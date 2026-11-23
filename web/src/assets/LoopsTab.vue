<script setup lang="ts">
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { useLoad } from '../api/useLoad';
import ScopeBadge from '../components/ScopeBadge.vue';
import { projectOf } from '../projects/api';
import { listLoops, plain } from './api';
import CommandSnippet from './CommandSnippet.vue';

// The Loops tab (handoff §3.5, #647): a LoopDetail per loop for the Project in the URL, sorted by
// name. Everything from a Source (description, stop, chip names) is untrusted and renders as text.
const route = useRoute();
const project = computed(() => projectOf(route.query));
const { state, data, failure, wait, skeleton, limited, retry } = useLoad(project, listLoops);
const loops = computed(() => data.value?.loops ?? []);
const chips = (l: (typeof loops.value)[number]) => [
  ...l.closure.agents.map((a) => ({ key: `agent:${a.name}`, kind: 'agent', name: a.name })),
  ...l.closure.skills.map((s) => ({ key: `skill:${s.name}`, kind: 'skill', name: s.name })),
];
</script>

<template>
  <p v-if="!project" class="note">Pick a Project from the selector to see its loops.</p>

  <div v-else-if="skeleton" class="list" aria-busy="true">
    <span role="status" class="sr-only">Loading loops</span>
    <div v-for="n in 2" :key="n" class="card bar"></div>
  </div>
  <template v-else-if="state === 'loading'" />

  <div v-else-if="state === 'error' && limited" class="box warning">
    <p role="status">
      <span class="sr-only">Too many requests. Try again shortly.</span>
      <span aria-hidden="true">{{ wait > 0 ? `Too many requests — try again in ${wait} s.` : 'Too many requests — you can retry now.' }}</span>
    </p>
    <button type="button" class="retry" :disabled="wait > 0" @click="retry">Retry</button>
  </div>
  <div v-else-if="state === 'error'" class="box danger" role="alert">
    <p>Couldn't load loops. {{ failure?.hint }}</p>
    <button type="button" class="retry" @click="retry">Retry</button>
  </div>

  <template v-else-if="state === 'ready'">
    <p v-if="data?.hint" class="note">{{ plain(data.hint) }}</p>
    <div v-if="!loops.length" role="status" class="box">
      <p>No loops for <code>{{ project }}</code> yet.</p>
    </div>
    <div v-else class="list">
      <article v-for="l in loops" :key="l.name" class="card">
        <div class="head">
          <h2>{{ l.name }}</h2>
          <ScopeBadge v-if="l.scope" :scope="plain(l.scope)" />
        </div>
        <p v-if="l.description" class="text">{{ plain(l.description) }}</p>
        <dl class="facts">
          <div v-if="l.dynamic || l.interval">
            <dt>Interval</dt>
            <dd>{{ l.dynamic ? 'dynamic' : plain(l.interval) }}</dd>
          </div>
          <div v-if="l.stop">
            <dt>Stops</dt>
            <dd>{{ plain(l.stop) }}</dd>
          </div>
        </dl>
        <ul v-if="chips(l).length" class="chips" aria-label="Closure">
          <li v-for="c in chips(l)" :key="c.key" class="chip">
            <span class="kind">{{ c.kind }}</span> <span class="cname">{{ plain(c.name) }}</span>
          </li>
        </ul>
        <CommandSnippet command="goldie pull" />
        <CommandSnippet :command="`/loop /goldie-${l.name}`" />
      </article>
    </div>
  </template>
</template>

<style scoped>
.note {
  max-width: 940px;
  margin: 0 0 var(--space-4);
  color: var(--fg-secondary);
}
.list {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  max-width: 940px;
}
.card {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-3);
  min-width: 0;
  padding: var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-1);
}
.bar {
  min-height: 160px;
  background: var(--surface-3);
}
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
  max-width: 100%;
}
h2 {
  margin: 0;
  font-family: var(--font-mono);
  font-size: var(--text-lg);
  overflow-wrap: anywhere;
}
.text,
dd {
  margin: 0;
  max-width: 100%;
  overflow-wrap: anywhere;
}
.facts {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  max-width: 100%;
  margin: 0;
}
.facts div {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
dt {
  color: var(--fg-secondary);
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  max-width: 100%;
  margin: 0;
  padding: 0;
  list-style: none;
}
.chip {
  min-width: 0;
  max-width: 100%;
  padding: 2px var(--space-2);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}
.kind {
  color: var(--fg-secondary);
}
.cname {
  font-family: var(--font-mono);
}
code {
  font-family: var(--font-mono);
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
