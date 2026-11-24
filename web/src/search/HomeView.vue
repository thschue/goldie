<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import HitCard from './HitCard.vue';
import HomeGuide from './HomeGuide.vue';
import { ApiError } from '../api/client';
import { ended } from '../auth/session';
import SearchField from './SearchField.vue';
import { queryOf, searchItems, searchKinds, type SearchHit } from './api';
import { projectOf } from '../projects/api';

// Home (handoff §3.2, §3.3): the question and the search field at `/`, the
// hits at `/?q=`. The query lives in the URL, so a search is shared by link and
// survives a reload. One GET /api/v1/search per query, Kind and Project: the
// server filters the Kind and applies the Project's Scope, so the Kind pills and
// the Project selector set URL parameters and search again (#730).
const route = useRoute();
const router = useRouter();

const query = computed(() => queryOf(route.query));
// Only a Kind the API accepts reaches the request; anything else in `?kind=` is All.
const kind = computed(() => {
  const k = route.query.kind;
  const v = Array.isArray(k) ? k[0] : k;
  return searchKinds.find((x) => x === v) ?? '';
});
const project = computed(() => projectOf(route.query));
const draft = ref(query.value);
const field = ref<InstanceType<typeof SearchField>>();

type State =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'done'; hits: SearchHit[]; hint?: string }
  | { kind: 'error'; message: string; hint?: string; limited?: boolean };
const state = ref<State>({ kind: 'idle' });
// The Kind pills stay mounted while the next search loads, so a pressed pill keeps focus.
const pillsUp = ref(false);
const retries = ref(0);
// A search still loading after 300 ms shows a skeleton (handoff §5.1: no flash for a fast answer).
// A 429 counts its Retry-After down, and Retry stays disabled meanwhile (#731).
const slow = ref(false);
const wait = ref(0);
let slowTimer: ReturnType<typeof setTimeout> | undefined;
let ticker: ReturnType<typeof setInterval> | undefined;
function settle() {
  clearTimeout(slowTimer);
  slow.value = false;
}

// Each search aborts the one before it, and only the newest may set the state:
// a slow older answer never overwrites a newer one, nor the Home that followed.
let current: AbortController | undefined;
async function search(q: string, k: string, p: string) {
  current?.abort();
  current = undefined;
  settle();
  clearInterval(ticker);
  wait.value = 0;
  if (!q) {
    state.value = { kind: 'idle' };
    pillsUp.value = false;
    return;
  }
  const mine = (current = new AbortController());
  state.value = { kind: 'loading' };
  slowTimer = setTimeout(() => (slow.value = true), 300);
  try {
    const { results, hint } = await searchItems(q, { kind: k, project: p }, mine.signal);
    if (current !== mine) return;
    settle();
    state.value = { kind: 'done', hits: results, hint };
    pillsUp.value = results.length > 0 || !!k;
  } catch (e) {
    if (current !== mine) return;
    settle();
    pillsUp.value = false;
    if (e instanceof ApiError && e.status === 429) {
      wait.value = e.retryAfter;
      ticker = setInterval(() => {
        if (--wait.value <= 0) clearInterval(ticker);
      }, 1000);
    }
    state.value =
      e instanceof ApiError
        ? { kind: 'error', message: e.message, hint: e.status === 401 && ended.value ? undefined : e.hint, limited: e.status === 429 }
        : { kind: 'error', message: "Couldn't load search results." };
  }
}

// The query of this view's own navigation in flight. The auth guard asks the
// session first, so it can land after the user has typed on: then the field
// keeps the newer words. Any other change of query (back, forward, a link)
// fills the field.
let pending: string | undefined;

watch(
  [query, kind, project, retries],
  ([q, k, p], old) => {
    if (q === pending) pending = undefined;
    else if (!old || q !== old[0]) draft.value = q;
    void search(q, k, p);
  },
  { immediate: true },
);

const hits = computed(() => (state.value.kind === 'done' ? state.value.hits : []));
// Submit in JavaScript, through the router: the UI runs under form-action 'none'.
// The Project stays in the address; a new query starts from All Kinds.
// One navigation at a time: a newer one would cancel an older one still in the
// guard, and that search would never reach the server or be recorded.
let navigating: Promise<void> = Promise.resolve();
function go(q: string, k = '') {
  navigating = navigating.then(async () => {
    pending = q === query.value ? undefined : q;
    const failure = await router.push({ path: '/', query: { ...(q && { q }), ...(q && k && { kind: k }), ...(project.value && { project: project.value }) } }).catch((e: unknown) => e);
    if (failure && pending === q) pending = undefined;
  });
  return navigating;
}

// Handoff §3.2 item 1. A chip runs the search as if the user had typed it.
const suggestions = ['database', 'logging', 'retries', 'feature flags'];

function pick(word: string) {
  draft.value = word;
  void go(word);
}

function submit() {
  void go(draft.value.trim());
}

async function clear() {
  draft.value = '';
  await go('');
  field.value?.focus();
}

onBeforeUnmount(() => {
  current?.abort();
  current = undefined;
  settle();
  clearInterval(ticker);
});
</script>

<template>
  <h1>What are you deciding today?</h1>
  <SearchField ref="field" v-model="draft" @submit="submit" @clear="clear" />

  <div v-if="pillsUp && (state.kind === 'loading' || state.kind === 'done')" class="pills" role="group" aria-label="Filter by Kind">
    <button type="button" class="pill" :aria-pressed="!kind" @click="go(query)">All</button>
    <button v-for="k in searchKinds" :key="k" type="button" class="pill" :aria-pressed="kind === k" @click="go(query, k)">{{ k }}</button>
  </div>

  <template v-if="state.kind === 'idle'">
    <p class="chips">
      <span>Try:</span>
      <button v-for="w in suggestions" :key="w" type="button" class="chip" @click="pick(w)">{{ w }}</button>
    </p>
    <HomeGuide />
  </template>

  <!-- Handoff §5.1: skeleton cards after 300 ms, never a spinner; nothing before. -->
  <div v-else-if="state.kind === 'loading'" :class="{ hits: slow }" :aria-busy="slow ? 'true' : undefined">
    <template v-if="slow">
      <span role="status" class="sr-only">Searching…</span>
      <div v-for="n in 3" :key="n" class="skeleton"></div>
    </template>
  </div>

  <div v-else-if="state.kind === 'error' && state.limited" class="alert warning">
    <p role="status">{{ wait > 0 ? `Too many requests — try again in ${wait} s.` : 'Too many requests — you can retry now.' }}</p>
    <button type="button" class="retry" :disabled="wait > 0" @click="retries++">Retry</button>
  </div>

  <div v-else-if="state.kind === 'error'" role="alert" class="alert">
    <p class="alert-message">{{ state.message }}</p>
    <p v-if="state.hint" class="alert-hint">{{ state.hint }}</p>
    <button type="button" class="retry" @click="retries++">Retry</button>
  </div>

  <template v-else-if="state.kind === 'done'">
    <!-- The API's hint for an unregistered Project, as served and as text. -->
    <p v-if="state.hint" class="hint-line">{{ state.hint }}</p>
    <!-- Handoff §5, "Empty Kind": a Kind that matches nothing is a neutral panel, not a Miss. -->
    <div v-if="!hits.length && kind" role="status" class="empty-kind">
      {{ project ? `No ${kind}s for ${project} yet.` : `No ${kind}s yet.` }}
    </div>
    <!-- Copy: handoff §8, "No results". Gold and dashed: a Miss is guidance wanted, not a failure. -->
    <div v-else-if="!hits.length" role="status" class="miss">
      <strong>No golden path for “{{ query }}” yet.</strong>
      Try other words — Items are found by their keywords. This search was recorded as a <strong>Miss</strong>, so the platform team knows
      guidance is wanted here.
    </div>
    <ul v-else class="hits">
      <HitCard v-for="hit in hits" :key="`${hit.kind}:${hit.id}`" :hit="hit" />
    </ul>
  </template>
</template>

<style scoped>
.skeleton {
  min-height: 96px;
  border-radius: var(--radius-md);
  background: var(--surface-3);
}
.chips {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  max-width: 940px;
  margin: 0 0 var(--space-6);
  color: var(--fg-secondary);
}
.chip {
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  border: 1px solid var(--border-strong);
  border-radius: 999px;
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
  cursor: pointer;
}
.miss,
.empty-kind,
.alert {
  max-width: 760px;
  padding: var(--space-6);
  border-radius: var(--radius-md);
}
.empty-kind {
  border: 1px solid var(--border-strong);
  background: var(--surface-1);
  color: var(--fg-secondary);
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
.warning {
  border-color: var(--miss-border);
  background: var(--miss-bg);
  color: var(--miss-ink);
}
.retry:disabled {
  opacity: 0.6;
  cursor: not-allowed;
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
.hint-line {
  max-width: 940px;
  margin: 0 0 var(--space-4);
  color: var(--fg-secondary);
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
</style>
