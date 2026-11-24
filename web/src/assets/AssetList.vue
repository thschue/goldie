<script setup lang="ts" generic="T extends { name: string }">
import { ref, watch } from 'vue';
import { listProjects } from '../projects/api';
import { plain } from './api';
import { sourceDocsURL } from './links';
import type { ApiError } from '../api/client';

// The load states every Agent assets tab shares (handoff §5.1, §5.2): the global view with no Project, skeletons,
// the 429 countdown, the error alert, the empty status and the card grid. A tab passes what
// useLoad answers and fills one card per item; a state fix is made here once.
const props = defineProps<{
  noun: string;
  project: string;
  state: string;
  failure?: ApiError;
  wait: number;
  skeleton: boolean;
  limited: boolean;
  retry: () => void;
  hint?: string;
  items: T[];
  // `grid` (default) is cards side by side; `list` is one full-width card per row (the Loops tab).
  layout?: 'grid' | 'list';
}>();

// With no Project selected the tab lists the global assets (#758). The list never waits on the
// Projects lookup (#788): its own load states decide what is drawn, and the lookup decides only
// whether the note to pick a Project shows. The tab stays mounted when only ?project= changes, so
// the lookup runs whenever none is selected and no answer is known yet (a failed lookup is tried
// again). While it is pending the note's line is reserved so the list does not jump when the note
// appears; a failed lookup says nothing.
const hasProjects = ref<boolean>();
const pending = ref(false);
watch(
  () => props.project,
  async (project) => {
    if (project || hasProjects.value !== undefined || pending.value) return;
    pending.value = true;
    try {
      hasProjects.value = (await listProjects()).length > 0;
    } catch {
      hasProjects.value = undefined;
    } finally {
      pending.value = false;
    }
  },
  { immediate: true },
);
</script>

<template>
  <p v-if="!project && hasProjects" class="note">Pick a Project from the selector to see its {{ noun }}.</p>
  <p v-else-if="!project && pending" class="note" aria-hidden="true"></p>

  <div v-if="skeleton" class="grid" :class="layout" aria-busy="true">
    <span role="status" class="sr-only">Loading {{ noun }}</span>
    <div v-for="n in 3" :key="n" class="card bar"></div>
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
    <p>Couldn't load {{ noun }}. {{ failure?.hint }}</p>
    <button type="button" class="retry" @click="retry">Retry</button>
  </div>

  <template v-else-if="state === 'ready'">
    <p v-if="hint" class="note">{{ plain(hint) }}</p>
    <div v-if="!items.length" role="status" class="box">
      <p v-if="project">No {{ noun }} for <code>{{ project }}</code> yet.</p>
      <p v-else>No {{ noun }} yet.</p>
      <a :href="sourceDocsURL" target="_blank" rel="noopener noreferrer">Source docs</a>
    </div>
    <ul v-else class="grid" :class="layout">
      <li v-for="item in items" :key="item.name" class="card">
        <slot :item="item" />
      </li>
    </ul>
  </template>
</template>

<style scoped>
.note {
  max-width: 940px;
  margin: 0 0 var(--space-4);
  color: var(--fg-secondary);
}
.note:empty {
  min-height: 1lh;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 280px), 1fr));
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}
.list {
  grid-template-columns: minmax(0, 940px);
}
.card {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-2);
  min-width: 0;
  padding: var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-1);
}
.bar {
  min-height: 120px;
  background: var(--surface-3);
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
.box a {
  display: inline-flex;
  margin-top: var(--space-2);
  align-items: center;
  min-height: var(--target-min);
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
code {
  font-family: var(--font-mono);
}
</style>
