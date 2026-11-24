<script setup lang="ts">
import { errorSentence } from '../api/errorSentence';
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { useLoad } from '../api/useLoad';
import { role } from '../auth/session';
import { plain } from '../assets/api';
import { ApiError, fallback } from '../api/client';
import { isFailing, loadSources, origin, state as badgeOf, resync, waitForSync, when, type SourceStatus } from './api';

// The Sources page (#633, #733, handoff §3.7, §5.3): a card per Source from GET /api/v1/sync,
// failing ones first, each with its own Resync (POST ?source=<name>). A reader gets the operators-only message and no request. The h1 renders
// at once so the focus the shell moves to it after navigation finds it.
const operator = computed(() => role() === 'operator');
const key = computed(() => (operator.value ? 'sync' : ''));
const { state, data, failure, skeleton, limited, wait, retry } = useLoad(key, (_k, signal) => loadSources(signal));
const forbidden = computed(() => !operator.value || (state.value === 'error' && failure.value?.status === 403));

// Resync (#634): the POST answers 202 at once, so the button stays "Syncing…" until a poll of the
// status shows the sync ended (or it times out), then the rows are replaced without a skeleton.
// The button stays focusable (aria-disabled, not disabled) so focus is not lost.
const rows = ref<SourceStatus[]>();
const syncing = ref(false);
const syncingName = ref<string>();
const syncError = ref('');
const note = ref('');
const stillRunning = ref(false);
const running = 'The sync is still running. Check back in a moment, or resync again.';
const stop = new AbortController();
onBeforeUnmount(() => stop.abort());
watch(data, (v) => {
  if (!v) return;
  rows.value = v;
  syncError.value = '';
});
async function startSync(name?: string) {
  if (syncing.value) return;
  syncing.value = true;
  syncingName.value = name;
  syncError.value = '';
  stillRunning.value = false;
  note.value = '';
  await nextTick(); // a changed text is what a live region announces
  note.value = 'Sync started. Waiting for it to end.';
  try {
    const before = rows.value ?? [];
    await resync(name);
    const done = await waitForSync(before, stop.signal);
    if (done.rows) rows.value = done.rows;
    stillRunning.value = !done.ended;
    note.value = done.ended ? 'Sync finished. Sources updated.' : running;
  } catch (e) {
    if (stop.signal.aborted) return;
    note.value = '';
    syncError.value = syncMessage(e);
  } finally {
    if (!stop.signal.aborted) syncing.value = false;
  }
}
const copied = ref<string>();
let copiedTimer: ReturnType<typeof setTimeout> | undefined;
onBeforeUnmount(() => clearTimeout(copiedTimer));
async function copy(revision: string) {
  try {
    await navigator.clipboard.writeText(revision);
    note.value = 'Revision copied.';
    copied.value = revision;
    clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => (copied.value = undefined), 2000);
  } catch {
    note.value = "Couldn't copy the revision.";
  }
}
function syncMessage(e: unknown) {
  if (!(e instanceof ApiError)) return "Couldn't start a sync. Try again in a moment.";
  if (e.message === fallback) return "Couldn't start a sync. Try again in a moment.";
  return e.hint ? `${errorSentence(e.message, e.hint)} ${e.hint}` : e.message;
}
</script>

<template>
  <h1>Sources</h1>
  <p v-if="!forbidden" class="intro">Failing Sources first. A failing Source keeps serving its last good Snapshot.</p>
  <span v-if="!forbidden" aria-live="polite" aria-atomic="true" class="sr-only">{{ note }}</span>
  <p v-if="stillRunning" class="intro" aria-hidden="true">{{ running }}</p>
  <div v-if="syncError" role="alert" class="box danger sync-error"><p>{{ syncError }}</p></div>
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
    <div v-if="!rows?.length" role="status" class="box"><p>No Source is configured.</p></div>
    <ul v-else class="list">
      <li v-for="(s, i) in rows" :key="s.name ?? i" class="row" :class="{ bad: isFailing(s) }">
        <div class="head">
          <h2>{{ s.name ? plain(s.name) : `Source ${s.preference ?? i + 1}` }}</h2>
          <strong class="badge">{{ badgeOf(s) }}</strong>
          <span class="chip">{{ origin(s.name) }}</span>{{ " " }}
          <span v-if="s.preference" class="chip">Order {{ s.preference }}</span>{{ " " }}
          <span class="chip">{{ s.shadowed?.length ?? 0 }} shadowed {{ s.shadowed?.length === 1 ? 'Item' : 'Items' }}</span>{{ " " }}
        </div>
        <p v-if="s.revision" class="rev">
          <code>{{ plain(s.revision).slice(0, 7) }}</code>
          <button type="button" :aria-label="`Copy revision${s.name ? ' of ' + plain(s.name) : ''}`" @click="copy(s.revision)">{{ copied === s.revision ? 'Copied' : 'Copy' }}</button>
        </p>
        <dl>
          <dt>Last poll</dt>
          <dd>{{ s.last_poll ? when(s.last_poll) : 'No poll has finished yet.' }}</dd>
          <dt>Last success</dt>
          <dd>{{ s.last_success ? when(s.last_success) : 'Never' }}</dd>
          <dt>Last error</dt>
          <dd class="err">{{ s.last_error ? plain(s.last_error) : 'None' }}</dd>
        </dl>
        <button
          type="button"
          class="sync"
          :aria-disabled="syncing"
          :aria-label="`${syncing && syncingName === s.name ? 'Syncing' : 'Resync'}${s.name ? ' ' + plain(s.name) : ''}`"
          @click="startSync(s.name)"
        >{{ syncing && syncingName === s.name ? 'Syncing…' : 'Resync' }}</button>
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
h2 {
  margin: 0;
  font-size: var(--text-lg);
}
.chip {
  padding: 2px var(--space-2);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
}
.rev {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin: 0 0 var(--space-2);
}
.sync {
  margin-top: var(--space-3);
}
.sync-error {
  margin: 0 0 var(--space-4);
}
button[aria-disabled='true'] {
  border-style: dashed;
  color: var(--fg-secondary);
  cursor: progress;
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
