<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import { useLoad } from '../api/useLoad';
import { plain } from '../assets/api';
import ScopeBadge from '../components/ScopeBadge.vue';
import { projectOf } from '../projects/api';
import { listDecisions, statusFilters, type StatusFilter } from './api';

// The Decisions page (handoff §3.8, #736): the decisions for the Project in the URL, accepted by
// default, with a status filter for the other lifecycles. Everything from a Source is untrusted
// and renders as text. The load key joins status and project, so a pill press or a Project change
// reloads; with no Project the global view is requested.
const route = useRoute();
const project = computed(() => projectOf(route.query));
const status = ref<StatusFilter>('accepted');
const key = computed(() => `${status.value}\n${project.value}`);
const { state, data, failure, wait, skeleton, limited, retry } = useLoad(key, (k, signal) => {
  const [s = 'accepted', p = ''] = k.split('\n');
  return listDecisions(s as StatusFilter, p, signal);
});
const decisions = computed(() => data.value?.decisions ?? []);
const supersession = (d: (typeof decisions.value)[number]) => [
  ...(d.superseded_by ? [`Superseded by ${plain(d.superseded_by)}`] : []),
  ...(d.supersedes.length ? [`Supersedes ${d.supersedes.map(plain).join(', ')}`] : []),
];
</script>

<template>
  <h1>Decisions</h1>
  <p class="lede">
    Decisions record why the platform is the way it is. <strong>Accepted decisions are binding for agents:</strong>
    if a change would contradict one, the agent stops and says so.
  </p>
  <div role="group" aria-label="Status" class="filter">
    <button
      v-for="f in statusFilters"
      :key="f.value"
      type="button"
      :aria-pressed="status === f.value"
      @click="status = f.value"
    >
      {{ f.label }}
    </button>
  </div>

  <div v-if="skeleton" class="list" aria-busy="true">
    <div v-for="n in 3" :key="n" class="card bar"></div>
  </div>
  <template v-else-if="state === 'loading' || state === 'idle'" />

  <div v-else-if="state === 'error' && limited" class="box warning">
    <p role="status">
      {{ wait > 0 ? `Too many requests — try again in ${wait} s.` : 'Too many requests — you can retry now.' }}
    </p>
    <button type="button" class="retry" :disabled="wait > 0" @click="retry">Retry</button>
  </div>
  <div v-else-if="state === 'error'" class="box danger" role="alert">
    <p>{{ failure?.message }} {{ failure?.hint }}</p>
    <button type="button" class="retry" @click="retry">Retry</button>
  </div>

  <template v-else-if="state === 'ready'">
    <p v-if="data?.hint" class="note">{{ plain(data.hint) }}</p>
    <div v-if="!decisions.length" role="status" class="box">
      <p>
        No decisions with this status and Scope. Proposed and rejected decisions are only shown when you ask for them — agents
        never see them.
      </p>
    </div>
    <ul v-else class="list">
      <li v-for="d in decisions" :key="d.name">
        <article class="card" :class="d.status">
          <div class="meta">
            <span class="status" :class="d.status">{{ d.status }}</span>
            <span v-if="d.status === 'accepted'" class="binding">binding</span>
            <ScopeBadge :scope="plain(d.scope)" />
            <time>{{ plain(d.date) }}</time>
          </div>
          <h2>{{ plain(d.title) }}</h2>
          <p v-for="line in supersession(d)" :key="line" class="note">{{ line }}</p>
        </article>
      </li>
    </ul>
  </template>
</template>

<style scoped>
.lede,
.note {
  max-width: 760px;
  color: var(--fg-secondary);
}
.note {
  margin: 0;
}
.filter {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin: var(--space-4) 0;
}
.filter button {
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  border: 1px solid var(--border-strong);
  border-radius: 999px;
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
  cursor: pointer;
}
.filter button[aria-pressed='true'] {
  border-color: var(--brand);
  background: var(--brand-tint);
  color: var(--brand-tint-ink);
  font-weight: 700;
}
.list {
  display: grid;
  gap: var(--space-3);
  max-width: 940px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.card {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
  padding: var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-1);
}
.card.proposed {
  border-style: dashed;
}
.bar {
  min-height: 90px;
  background: var(--surface-3);
}
.meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
.status,
.binding {
  padding: 2px var(--space-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
}
.status.accepted {
  border-color: var(--brand-tint-ink);
  background: var(--brand-tint-ink);
  color: var(--surface-1);
}
.status.proposed {
  border-style: dashed;
}
.binding {
  border-color: var(--brand-tint-ink);
  color: var(--brand-tint-ink);
}
h2 {
  margin: 0;
  font-size: var(--text-lg);
  overflow-wrap: anywhere;
}
.superseded h2 {
  text-decoration: line-through;
  color: var(--fg-secondary);
}
.rejected h2 {
  color: var(--fg-secondary);
}
.box {
  max-width: 940px;
  padding: var(--space-4) var(--space-6);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-2);
  color: var(--fg-secondary);
}
.box p {
  margin: 0;
}
.danger,
.warning {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
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
</style>
