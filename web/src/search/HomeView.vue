<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import HitCard from './HitCard.vue';
import { searchItems, SearchError, type SearchHit } from './api';

// Home (handoff §3.2, §3.3): the question and the search field at `/`, the
// hits at `/?q=`. The query lives in the URL, so a search is shared by link and
// survives a reload. One GET /api/v1/search per query; the Kind filter works
// on that response in the browser.
const route = useRoute();
const router = useRouter();

const query = computed(() => {
  const q = route.query.q;
  return (Array.isArray(q) ? q[0] : q)?.trim() ?? '';
});
const draft = ref(query.value);
const input = ref<HTMLInputElement>();

type State =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'done'; hits: SearchHit[] }
  | { kind: 'error'; message: string; hint?: string };
const state = ref<State>({ kind: 'idle' });
const kindFilter = ref('all');
const retries = ref(0);

// Each search aborts the one before it, and only the newest may set the state:
// a slow older answer never overwrites a newer one, nor the Home that followed.
let current: AbortController | undefined;
async function search(q: string) {
  current?.abort();
  current = undefined;
  kindFilter.value = 'all';
  if (!q) {
    state.value = { kind: 'idle' };
    return;
  }
  const mine = (current = new AbortController());
  state.value = { kind: 'loading' };
  try {
    const { results } = await searchItems(q, mine.signal);
    if (current === mine) state.value = { kind: 'done', hits: results };
  } catch (e) {
    if (current !== mine) return;
    state.value =
      e instanceof SearchError ? { kind: 'error', message: e.message, hint: e.hint } : { kind: 'error', message: "Couldn't load search results." };
  }
}

watch(
  [query, retries],
  ([q], old) => {
    if (!old || q !== old[0]) draft.value = q;
    void search(q);
  },
  { immediate: true },
);

const hits = computed(() => (state.value.kind === 'done' ? state.value.hits : []));
// A pill per Kind that has a hit, in the order the Kinds first appear.
const kinds = computed(() => {
  const counts = new Map<string, number>();
  for (const h of hits.value) counts.set(h.kind, (counts.get(h.kind) ?? 0) + 1);
  return [...counts];
});
const shown = computed(() => (kindFilter.value === 'all' ? hits.value : hits.value.filter((h) => h.kind === kindFilter.value)));

// Submit in JavaScript, through the router: the UI runs under form-action 'none'.
function submit() {
  const q = draft.value.trim();
  void router.push(q ? { path: '/', query: { q } } : { path: '/' });
}

async function clear() {
  draft.value = '';
  await router.push({ path: '/' });
  input.value?.focus();
}

// "/" focuses the field from anywhere but a place that takes typing.
function onKeydown(e: KeyboardEvent) {
  if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return;
  const t = e.target;
  if (t instanceof HTMLElement && (t.isContentEditable || /^(input|textarea|select)$/i.test(t.tagName))) return;
  e.preventDefault();
  input.value?.focus();
}
onMounted(() => document.addEventListener('keydown', onKeydown));
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown);
  current?.abort();
  current = undefined;
});
</script>

<template>
  <h1>What are you deciding today?</h1>
  <form class="search" role="search" @submit.prevent="submit">
    <label for="home-search" class="label">Search the golden path</label>
    <div class="field">
      <input
        id="home-search"
        ref="input"
        v-model="draft"
        type="search"
        class="input"
        placeholder="database, logging, retries, feature flags…"
        autocomplete="off"
        aria-keyshortcuts="/"
      />
      <button v-if="draft" type="button" class="icon-button clear" aria-label="Clear search" @click="clear">
        <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true" focusable="false"><path d="M6 6l12 12M18 6L6 18" /></svg>
      </button>
      <kbd v-else class="hint" aria-hidden="true">/</kbd>
    </div>
  </form>

  <!-- Copy: handoff §8, "Empty state". Home's sections below the search (§3.2)
       aren't built yet, so Home with no query shows the neutral panel, never gold. -->
  <div v-if="state.kind === 'idle'" role="status" class="panel">Nothing to show yet. Content appears here as goldie's pages come online.</div>

  <p v-else-if="state.kind === 'loading'" class="loading" role="status">Searching…</p>

  <div v-else-if="state.kind === 'error'" role="alert" class="alert">
    <p class="alert-message">{{ state.message }}</p>
    <p v-if="state.hint" class="alert-hint">{{ state.hint }}</p>
    <button type="button" class="retry" @click="retries++">Retry</button>
  </div>

  <!-- Copy: handoff §8, "No results". Gold and dashed: a Miss is guidance wanted, not a failure. -->
  <div v-else-if="state.kind === 'done' && !hits.length" role="status" class="miss">
    <strong>No golden path for “{{ query }}” yet.</strong>
    Try other words — Items are found by their keywords. This search was recorded as a <strong>Miss</strong>, so the platform team knows
    guidance is wanted here.
  </div>

  <template v-else-if="state.kind === 'done'">
    <div class="pills" role="group" aria-label="Filter by Kind">
      <button type="button" class="pill" :aria-pressed="kindFilter === 'all'" @click="kindFilter = 'all'">All {{ hits.length }}</button>
      <button v-for="[kind, count] in kinds" :key="kind" type="button" class="pill" :aria-pressed="kindFilter === kind" @click="kindFilter = kind">
        {{ kind }} {{ count }}
      </button>
    </div>
    <ul class="hits">
      <HitCard v-for="hit in shown" :key="`${hit.kind}:${hit.id}`" :hit="hit" />
    </ul>
  </template>
</template>

<style scoped>
.search {
  max-width: 940px;
  margin-bottom: var(--space-6);
}
.label {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
.field {
  position: relative;
}
.input {
  width: 100%;
  height: 58px;
  padding: 0 calc(var(--target-min) + var(--space-3)) 0 var(--space-4);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
}
.input::placeholder {
  color: var(--fg-muted);
}
.clear {
  position: absolute;
  top: 7px;
  right: var(--space-2);
  border: 0;
}
.hint {
  position: absolute;
  top: 50%;
  right: var(--space-4);
  transform: translateY(-50%);
  padding: 0 var(--space-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
.loading {
  color: var(--fg-secondary);
}
.panel {
  max-width: 760px;
  padding: var(--space-6);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-2);
  color: var(--fg-secondary);
}
.miss,
.alert {
  max-width: 760px;
  padding: var(--space-6);
  border-radius: var(--radius-md);
}
.miss {
  border: 1px dashed var(--miss-line);
  background: var(--miss-bg);
  color: var(--miss-ink);
}
.miss strong:first-child {
  display: block;
  margin-bottom: var(--space-2);
}
.alert {
  border: 1px solid var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-ink);
}
.alert p {
  margin: 0 0 var(--space-2);
}
.alert-message {
  font-weight: 600;
}
.retry {
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  border: 1px solid currentColor;
  border-radius: var(--radius-md);
  background: transparent;
  color: inherit;
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}
.pills {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  max-width: 940px;
  margin-bottom: var(--space-4);
}
.pill {
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  border: 1px solid var(--border-strong);
  border-radius: 999px;
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
  cursor: pointer;
}
.pill[aria-pressed='true'] {
  border-color: var(--brand);
  background: var(--brand-tint);
  color: var(--brand-tint-ink);
  font-weight: 600;
}
.hits {
  display: grid;
  gap: var(--space-3);
  max-width: 940px;
  margin: 0;
  padding: 0;
}

/* §7: the field is 52 px below 768 px; the clear button stays centred. */
@media (max-width: 767px) {
  .input {
    height: 52px;
  }
  .clear {
    top: calc((52px - var(--target-min)) / 2);
  }
}
</style>
