<script setup lang="ts">
import { computed } from 'vue';
import { useLoad } from '../api/useLoad';
import { role } from '../auth/session';
import { plain } from '../assets/api';
import { isFailing, loadSources, when } from './api';

// The Sources page (#633, handoff §3.7, §5.3): a row per Source from GET /api/v1/sync, failing
// ones first. Read-only. A reader gets the operators-only message and no request. The h1 renders
// at once so the focus the shell moves to it after navigation finds it.
const operator = computed(() => role() === 'operator');
const key = computed(() => (operator.value ? 'sync' : ''));
const { state, data, failure, skeleton, limited, wait, retry } = useLoad(key, (_k, signal) => loadSources(signal));
const forbidden = computed(() => !operator.value || (state.value === 'error' && failure.value?.status === 403));
</script>

<template>
  <h1>Sources</h1>
  <p v-if="!forbidden" class="intro">Failing Sources first. A failing Source keeps serving its last good Snapshot.</p>
  <div v-if="forbidden" role="status" class="box"><p>This page is for operators.</p></div>

  <div v-else-if="skeleton" aria-busy="true" class="list">
    <span role="status" class="sr-only">Loading Sources</span>
    <div v-for="n in 2" :key="n" class="row bar"></div>
  </div>
  <template v-else-if="state === 'loading'" />

  <div v-else-if="state === 'error' && limited" class="box warning">
    <p role="status">
      <span class="sr-only">Too many requests. Try again shortly.</span>
      <span aria-hidden="true">{{ wait > 0 ? `Too many requests — try again in ${wait} s.` : 'Too many requests — you can retry now.' }}</span>
    </p>
    <button type="button" :disabled="wait > 0" @click="retry">Retry</button>
  </div>
  <div v-else-if="state === 'error'" role="alert" class="box danger">
    <p>Couldn't load Sources. {{ failure?.hint }}</p>
    <button type="button" @click="retry">Retry</button>
  </div>

  <template v-else-if="state === 'ready'">
    <div v-if="!data?.length" role="status" class="box"><p>No Source is configured.</p></div>
    <ul v-else class="list">
      <li v-for="(s, i) in data" :key="i" class="row" :class="{ bad: isFailing(s) }">
        <p class="head">
          <code>{{ s.revision ? plain(s.revision).slice(0, 7) : 'no revision' }}</code>
          <strong v-if="isFailing(s)" class="badge">Failing</strong>
        </p>
        <dl>
          <dt>Last poll</dt>
          <dd>{{ s.last_poll ? when(s.last_poll) : 'No poll has finished yet.' }}</dd>
          <dt>Last success</dt>
          <dd>{{ s.last_success ? when(s.last_success) : 'Never' }}</dd>
          <dt>Last error</dt>
          <dd class="err">{{ s.last_error ? plain(s.last_error) : 'None' }}</dd>
        </dl>
      </li>
    </ul>
  </template>
</template>

<style scoped>
.intro {
  max-width: 940px;
  margin: 0 0 var(--space-4);
  color: var(--fg-secondary);
}
.list {
  display: grid;
  gap: var(--space-3);
  max-width: 940px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.row {
  min-width: 0;
  padding: var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-1);
}
.row.bad {
  border-color: var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-ink);
}
.bar {
  min-height: 96px;
  background: var(--surface-3);
}
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
  margin: 0 0 var(--space-2);
}
code {
  font-family: var(--font-mono);
  font-size: var(--text-lg);
}
.badge {
  padding: 2px var(--space-2);
  border: 1px solid currentColor;
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
}
dl {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: var(--space-1) var(--space-4);
  margin: 0;
}
dt {
  font-weight: 600;
}
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.err {
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
button {
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  border: 1px solid currentColor;
  border-radius: var(--radius-sm);
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}
</style>
