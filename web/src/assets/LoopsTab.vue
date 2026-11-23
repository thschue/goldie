<script setup lang="ts">
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { useLoad } from '../api/useLoad';
import ScopeBadge from '../components/ScopeBadge.vue';
import { projectOf } from '../projects/api';
import { listLoops, plain } from './api';
import AssetList from './AssetList.vue';
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
  <AssetList
    noun="loops"
    layout="list"
    :project="project"
    :state="state"
    :failure="failure"
    :wait="wait"
    :skeleton="skeleton"
    :limited="limited"
    :retry="retry"
    :hint="data?.hint"
    :items="loops"
  >
    <template #default="{ item: l }">
      <article class="loop">
        <div class="head">
          <h2>{{ plain(l.name) }}</h2>
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
        <CommandSnippet :command="`/loop /goldie-${plain(l.name)}`" />
      </article>
    </template>
  </AssetList>
</template>

<style scoped>
.loop {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-3);
  min-width: 0;
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
</style>
