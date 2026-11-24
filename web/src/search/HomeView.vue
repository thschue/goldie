<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import HitCard from './HitCard.vue';
import HomeGuide from './HomeGuide.vue';
import { ApiError } from '../api/client';
import { useLoad } from '../api/useLoad';
import { ended } from '../auth/session';
import SearchField from './SearchField.vue';
import { queryOf, searchItems, searchKinds } from './api';
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

// One load through useLoad (#587): each search aborts the one before it, only the newest sets the
// state, a search still loading after 300 ms shows the skeleton, and a 429 counts its Retry-After down.
// The key is the search; no query is no search.
const key = computed(() => (query.value ? JSON.stringify([query.value, kind.value, project.value]) : ''));
const { state, data, failure, wait, skeleton, limited, retry } = useLoad(key, async (k, signal) => {
  const [q, kd, p] = JSON.parse(k) as [string, string, string];
  try {
    return await searchItems(q, { kind: kd, project: p }, signal);
  } catch (e) {
    throw e instanceof ApiError ? e : new ApiError(0, "Couldn't load search results.");
  }
});
// The Kind pills stay mounted while the next search loads, so a pressed pill keeps focus.
const pillsUp = ref(false);
watch(state, (s) => {
  if (s === 'ready') pillsUp.value = (data.value?.results.length ?? 0) > 0 || !!kind.value;
  else if (s === 'error' || s === 'idle') pillsUp.value = false;
});

// The query of this view's own navigation in flight. The auth guard asks the
// session first, so it can land after the user has typed on: then the field
// keeps the newer words. Any other change of query (back, forward, a link)
// fills the field.
let pending: string | undefined;

watch(
  query,
  (q, old) => {
    if (q === pending) pending = undefined;
    else if (old === undefined || q !== old) draft.value = q;
  },
  { immediate: true },
);

// A 401 that ended a signed-in session says so elsewhere; its hint is not repeated here.
const hint = computed(() => (failure.value?.status === 401 && ended.value ? undefined : failure.value?.hint));
const hits = computed(() => data.value?.results ?? []);
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

</script>

<template>
  <h1>What are you deciding today?</h1>
  <SearchField ref="field" v-model="draft" @submit="submit" @clear="clear" />

  <div v-if="pillsUp && (state === 'loading' || state === 'ready')" class="pills" role="group" aria-label="Filter by Kind">
    <button type="button" class="pill" :aria-pressed="!kind" @click="go(query)">All</button>
    <button v-for="k in searchKinds" :key="k" type="button" class="pill" :aria-pressed="kind === k" @click="go(query, k)">{{ k }}</button>
  </div>

  <template v-if="state === 'idle'">
    <p class="chips">
      <span>Try:</span>
      <button v-for="w in suggestions" :key="w" type="button" class="chip" @click="pick(w)">{{ w }}</button>
    </p>
    <HomeGuide />
  </template>

  <!-- Handoff §5.1: skeleton cards after 300 ms, never a spinner; nothing before. -->
  <div v-else-if="skeleton" class="hits" aria-busy="true">
    <span role="status" class="sr-only">Searching…</span>
    <div v-for="n in 3" :key="n" class="skeleton"></div>
  </div>
  <template v-else-if="state === 'loading'" />

  <div v-else-if="state === 'error' && limited" class="alert warning">
    <!-- The sentence is announced once; the live number is hidden from the screen reader. -->
    <p role="status">
      <span class="sr-only">Too many requests. Try again shortly.</span>
      <span aria-hidden="true">{{ wait > 0 ? `Too many requests — try again in ${wait} s.` : 'Too many requests — you can retry now.' }}</span>
    </p>
    <button type="button" class="retry" :disabled="wait > 0" @click="retry">Retry</button>
  </div>

  <div v-else-if="state === 'error'" role="alert" class="alert">
    <p class="alert-message">{{ failure?.message }}</p>
    <p v-if="hint" class="alert-hint">{{ hint }}</p>
    <button type="button" class="retry" @click="retry">Retry</button>
  </div>

  <template v-else-if="state === 'ready'">
    <!-- The API's hint for an unregistered Project, as served and as text. -->
    <p v-if="data?.hint" class="hint-line">{{ data.hint }}</p>
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
