<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import EmptyView from '../views/EmptyView.vue';
import { ApiError, projectItems, projectOf, type ProjectView } from './api';

// Home with a Project (`/?project=host/org/repo`, handoff §3.2, §5.1, §5.2): its Items, as text only.
const route = useRoute();
const project = computed(() => projectOf(route.query));

type State = 'loading' | 'ready' | 'error' | 'notfound';
const state = ref<State>('loading');
const view = ref<ProjectView>();
const failure = ref<ApiError>();
const wait = ref(0);
let controller: AbortController | undefined;
let timer: ReturnType<typeof setInterval> | undefined;

const stopCountdown = () => clearInterval(timer);

function countdown(seconds: number) {
  stopCountdown();
  wait.value = seconds;
  timer = setInterval(() => {
    if (--wait.value <= 0) stopCountdown();
  }, 1000);
}

async function load() {
  controller?.abort();
  stopCountdown();
  wait.value = 0;
  if (!project.value) return;
  const mine = (controller = new AbortController());
  state.value = 'loading';
  view.value = undefined;
  try {
    const v = await projectItems(project.value, mine.signal);
    if (mine.signal.aborted) return;
    view.value = v;
    state.value = 'ready';
  } catch (e) {
    if (mine.signal.aborted) return;
    if (e instanceof ApiError && (e.status === 404 || e.status === 400)) {
      state.value = 'notfound';
      return;
    }
    failure.value = e instanceof ApiError ? e : new ApiError(0, "Couldn't load this.");
    if (failure.value.status === 429) countdown(failure.value.retryAfter);
    state.value = 'error';
  }
}

watch(project, load, { immediate: true });
onBeforeUnmount(() => {
  controller?.abort();
  stopCountdown();
});

const limited = computed(() => failure.value?.status === 429);
const items = computed(() => view.value?.items ?? []);
const heading = computed(() => (state.value === 'notfound' ? 'Project not found' : 'What are you deciding today?'));
</script>

<template>
  <EmptyView v-if="!project" />
  <template v-else>
    <h1>{{ heading }}</h1>

    <template v-if="state === 'notfound'">
      <p class="lead">There's no Project at this address. Pick one from the Project selector, or start again from Home.</p>
      <RouterLink to="/" class="home">Go to Home</RouterLink>
    </template>

    <div v-else-if="state === 'loading'" class="skeleton" aria-busy="true">
      <span role="status" class="sr-only">Loading Items</span>
      <div v-for="n in 4" :key="n" class="bar"></div>
    </div>

    <!-- §5.1: a rate limit is an inline warning with a countdown; any other error is the danger Alert. -->
    <div v-else-if="state === 'error' && limited" class="box warning">
      <p role="status">
        <span class="sr-only">Too many requests. Try again shortly.</span>
        <span aria-hidden="true">{{ wait > 0 ? `Too many requests — try again in ${wait} s.` : 'Too many requests — you can retry now.' }}</span>
      </p>
      <button type="button" class="retry" :disabled="wait > 0" @click="load">Retry</button>
    </div>
    <div v-else-if="state === 'error'" class="box danger" role="alert">
      <p>{{ failure?.message }} {{ failure?.hint }}</p>
      <button type="button" class="retry" @click="load">Retry</button>
    </div>

    <template v-else>
      <p class="lead">Same golden path your agents follow — {{ items.length }} {{ items.length === 1 ? 'Item' : 'Items' }} for <span class="mono">{{ project }}</span>.</p>
      <p v-if="view?.hint" class="hint">{{ view.hint }}</p>
      <div v-if="!items.length" role="status" class="panel">No articles for {{ project }} yet.</div>
      <ul v-else class="items">
        <li v-for="item in items" :key="item.id" class="item">
          <span class="badge kind">article</span>
          <bdi class="name">{{ item.name }}</bdi>
          <span class="badge">{{ item.scope }}</span>
          <span class="badge" :class="item.status === 'deprecated' ? 'deprecated' : 'published'">
            <span>{{ item.status }}</span>
            <span v-if="item.replaced_by"> → <code>{{ item.replaced_by }}</code></span>
          </span>
          <span v-if="item.enforced" class="badge">enforced</span>
        </li>
      </ul>
    </template>
  </template>
</template>

<style scoped>
.lead,
.hint {
  max-width: 940px;
  margin: 0 0 var(--space-4);
  color: var(--fg-secondary);
}
.home {
  display: inline-block;
  padding: var(--space-3) 0;
}
.panel,
.box {
  max-width: 940px;
  padding: var(--space-4) var(--space-6);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-2);
  color: var(--fg-secondary);
}
.box {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
}
.box p {
  margin: 0;
}
.danger {
  border-color: var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-ink);
}
.warning {
  border-color: var(--miss-border);
  background: var(--miss-bg);
  color: var(--miss-ink);
}
.retry {
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  border: 1px solid currentColor;
  border-radius: var(--radius-sm);
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
  font-weight: 600;
}
.skeleton,
.items {
  display: grid;
  gap: var(--space-3);
  max-width: 940px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.bar {
  height: 64px;
  border-radius: var(--radius-md);
  background: var(--surface-3);
}
.item {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
  padding: var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-1);
}
.mono,
code {
  font-family: var(--font-mono);
}
.name {
  font-family: var(--font-mono);
  flex: 1 1 200px;
  min-width: 0;
  overflow-wrap: anywhere;
  font-weight: 600;
}
.badge {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 2px var(--space-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
.kind {
  border-color: transparent;
  background: var(--brand-tint);
  color: var(--brand-tint-ink);
  font-family: var(--font-mono);
}
.published {
  border-color: transparent;
  background: var(--ok-bg);
  color: var(--ok-ink);
}
.deprecated {
  border-color: var(--miss-border);
  background: var(--miss-bg);
  color: var(--miss-ink);
}
</style>
