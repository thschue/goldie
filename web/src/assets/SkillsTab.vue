<script setup lang="ts">
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { useLoad } from '../api/useLoad';
import ScopeBadge from '../components/ScopeBadge.vue';
import { projectOf } from '../projects/api';
import { listSkills, plain, promptName } from './api';
import { sourceDocsURL } from './links';

// The Skills tab (handoff §3.5, §5.1, §5.2): a card per skill for the Project in the URL,
// sorted by name. Descriptions are Source text and render as text only.
const route = useRoute();
const project = computed(() => projectOf(route.query));
const { state, data, failure, wait, skeleton, limited, retry } = useLoad(project, listSkills);
const skills = computed(() => data.value?.skills ?? []);
</script>

<template>
  <p v-if="!project" class="note">Pick a Project from the selector to see its skills.</p>

  <div v-else-if="skeleton" class="grid" aria-busy="true">
    <span role="status" class="sr-only">Loading skills</span>
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
    <p>Couldn't load skills. {{ failure?.hint }}</p>
    <button type="button" class="retry" @click="retry">Retry</button>
  </div>

  <template v-else-if="state === 'ready'">
    <p v-if="data?.hint" class="note">{{ plain(data.hint) }}</p>
    <div v-if="!skills.length" role="status" class="box">
      <p>No skills for <code>{{ project }}</code> yet.</p>
      <a :href="sourceDocsURL" target="_blank" rel="noopener noreferrer">Source docs</a>
    </div>
    <ul v-else class="grid">
      <li v-for="s in skills" :key="s.name" class="card">
        <div class="head">
          <h2>{{ s.name }}</h2>
          <ScopeBadge v-if="s.scope" :scope="plain(s.scope)" />
        </div>
        <p v-if="s.description" class="desc">{{ plain(s.description) }}</p>
        <code class="prompt">{{ promptName(s.name) }}</code>
        <template v-if="s.requires_sync">
          <span class="sync">requires_sync</span>
          <p class="pull">Contains scripts, so agents won't run it from text. Run <code>goldie pull</code> to install it.</p>
        </template>
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
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 280px), 1fr));
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
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
.desc,
.pull {
  margin: 0;
  max-width: 100%;
  overflow-wrap: anywhere;
}
.desc {
  color: var(--fg-default);
}
.pull {
  color: var(--miss-ink);
  font-size: var(--text-sm);
}
code {
  font-family: var(--font-mono);
}
.prompt {
  max-width: 100%;
  overflow-wrap: anywhere;
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
.sync {
  padding: 2px var(--space-2);
  border: 1px solid var(--miss-border);
  border-radius: var(--radius-sm);
  background: var(--miss-bg);
  color: var(--miss-ink);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
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
</style>
