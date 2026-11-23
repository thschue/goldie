<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { ApiError } from '../api/client';
import { plain } from '../assets/api';
import { add, announceEvery, queryText, resultText, timeText } from './rows';
import { streamActivity, type ActivityEvent, type Frame } from './stream';

// The activity feed on the local stream (handoff §3.6, §5.1, §5.3, §9; #630). One table of the
// local replica's events, newest first, kept to the newest maxRows. New rows never take focus:
// nothing here calls focus(), and the rows are keyed by event id so a selection stays on its
// event. The miss count is a polite status that changes at most every 30 s. Out of scope here:
// the replica switcher, the Miss board and detail panel, and the live stage.
const rows = ref<ActivityEvent[]>([]);
const phase = ref<'connecting' | 'live' | 'error'>('connecting');
const failed = ref<ApiError | Error>();
const dropped = ref(false);
const paused = ref(false);
const missesOnly = ref(false);
const selected = ref<number>();
let held: ActivityEvent[] = [];
let controller: AbortController | undefined;

const shown = computed(() => (missesOnly.value ? rows.value.filter((r) => r.miss) : rows.value));
const missCount = computed(() => rows.value.filter((r) => r.miss).length);
const replica = computed(() => plain(rows.value[0]?.replica));

function apply(frames: Frame[]) {
  const events: ActivityEvent[] = [];
  for (const f of frames) {
    if (f.type === 'event') events.push(f.event);
    else if (f.type === 'dropped') dropped.value = true;
    else if (phase.value === 'connecting') phase.value = 'live';
  }
  if (!events.length) return;
  if (paused.value) held = add(held, events);
  else rows.value = add(rows.value, events);
}

async function connect() {
  controller?.abort();
  const mine = (controller = new AbortController());
  rows.value = [];
  held = [];
  dropped.value = false;
  failed.value = undefined;
  phase.value = 'connecting';
  try {
    await streamActivity(mine.signal, (frames) => !mine.signal.aborted && apply(frames));
    if (!mine.signal.aborted) throw new Error('The stream ended.');
  } catch (e) {
    if (mine.signal.aborted) return;
    failed.value = e instanceof ApiError ? e : new Error("goldie isn't answering right now.");
    phase.value = 'error';
  }
}

function togglePause() {
  paused.value = !paused.value;
  if (!paused.value) {
    rows.value = add(rows.value, [...held].reverse());
    held = [];
  }
}

const select = (id: number) => (selected.value = selected.value === id ? undefined : id);

// The announced miss count: set when the history ends, then at most once per announceEvery.
const announced = ref(0);
let announcedAt = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
function announce() {
  clearTimeout(timer);
  timer = undefined;
  announced.value = missCount.value;
  announcedAt = Date.now();
}
watch(phase, (p) => p === 'live' && announce());
watch(missCount, () => {
  if (phase.value !== 'live' || timer || missCount.value === announced.value) return;
  const wait = announcedAt + announceEvery - Date.now();
  if (wait <= 0) announce();
  else timer = setTimeout(announce, wait);
});

onMounted(connect);
onBeforeUnmount(() => {
  controller?.abort();
  clearTimeout(timer);
});

const status = computed(() => (failed.value instanceof ApiError ? failed.value.status : 0));
const hint = computed(() => (failed.value instanceof ApiError ? plain(failed.value.hint) : ''));
</script>

<template>
  <h1>Activity</h1>

  <p v-if="status === 403" class="box">This page is for operators.</p>
  <p v-else-if="status === 401" class="box">Sign in to see what is being asked of goldie.</p>
  <div v-else-if="phase === 'error'" class="box danger" role="alert">
    <p v-if="status === 429">Too many requests. Try again in a moment.</p>
    <p v-else-if="status === 503">Couldn't open the activity feed. {{ hint }}</p>
    <p v-else>Couldn't read the activity feed. {{ hint || 'Check your connection, then try again.' }}</p>
    <button type="button" class="control" @click="connect">Retry</button>
  </div>

  <template v-else>
    <div class="bar">
      <p class="label">
        <template v-if="phase === 'connecting'">Connecting to the local replica…</template>
        <template v-else>
          Live · local replica<template v-if="replica"> <strong>{{ replica }}</strong></template>
        </template>
      </p>
      <button type="button" class="control" @click="togglePause">{{ paused ? 'Resume' : 'Pause' }}</button>
      <button type="button" class="control" :aria-pressed="missesOnly" @click="missesOnly = !missesOnly">
        <span v-if="missesOnly" aria-hidden="true">✓ </span>Misses only
      </button>
      <p role="status" class="misses">{{ announced }} {{ announced === 1 ? 'miss' : 'misses' }} in this feed</p>
    </div>

    <div role="status">
      <p v-if="dropped" class="box warning">Some events were missed because this page fell behind.</p>
    </div>

    <p v-if="phase === 'live' && !rows.length" class="box">
      No requests yet. Run <code>goldie search</code> or ask your agent something.
    </p>
    <p v-else-if="phase === 'live' && !shown.length" class="box">
      Nothing matches these filters in the last five minutes.
    </p>
    <div v-if="shown.length" class="scroll" role="region" aria-label="Activity feed" tabindex="0">
      <table>
        <thead>
          <tr>
            <th scope="col">Time</th>
            <th scope="col">Event</th>
            <th scope="col">Query → Item</th>
            <th scope="col">Result</th>
            <th scope="col">Via</th>
            <th scope="col">Project</th>
            <th scope="col">Replica</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="e in shown" :key="`${e.replica}/${e.id}`" :class="{ miss: e.miss, selected: selected === e.id }">
            <td>
              <button type="button" class="row" :aria-pressed="selected === e.id" :aria-describedby="`q-${e.id}`" @click="select(e.id)">
                {{ timeText(e.time) }}
              </button>
            </td>
            <td>{{ e.kind }}</td>
            <td :id="`q-${e.id}`" class="query">{{ queryText(e) }}</td>
            <td :class="{ result: e.miss }">{{ resultText(e) }}</td>
            <td><span aria-hidden="true">—</span><span class="sr-only">Not recorded</span></td>
            <td class="wrap">{{ plain(e.project) || '—' }}</td>
            <td class="wrap">{{ plain(e.replica) || '—' }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </template>
</template>

<style scoped>
.bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
  margin-bottom: var(--space-4);
}
.label,
.misses {
  margin: 0;
  color: var(--fg-secondary);
}
.label {
  margin-right: auto;
}
.control {
  min-width: var(--target-min);
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}
.control[aria-pressed='true'] {
  border-width: 2px;
  background: var(--miss-solid);
  color: var(--miss-solid-ink);
}
.box {
  max-width: 760px;
  margin: 0 0 var(--space-4);
  padding: var(--space-4) var(--space-6);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-2);
  color: var(--fg-secondary);
}
.danger,
.warning {
  border-color: var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-ink);
}
.warning {
  border-color: var(--miss-border);
  background: var(--miss-bg);
  color: var(--miss-ink);
}
.danger {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
}
.danger p {
  margin: 0;
}
code {
  font-family: var(--font-mono);
}
.scroll {
  /* The containing block of the .sr-only cells, so they scroll with the table, not the page. */
  position: relative;
  overflow-x: auto;
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
}
table {
  width: 100%;
  min-width: 760px;
  border-collapse: collapse;
  font-size: var(--text-sm);
}
th,
td {
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid var(--border-subtle);
  text-align: left;
  vertical-align: middle;
}
th {
  background: var(--surface-2);
  color: var(--fg-secondary);
  font-weight: 600;
}
.query {
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
  unicode-bidi: isolate;
}
.wrap {
  overflow-wrap: anywhere;
}
tr.miss {
  background: var(--miss-bg);
}
.result {
  color: var(--miss-ink);
  font-weight: 600;
}
.row {
  min-width: var(--target-min);
  min-height: var(--target-min);
  padding: 0 var(--space-3);
  border: 1px solid transparent;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--brand);
  font: inherit;
  font-variant-numeric: tabular-nums;
  cursor: pointer;
}
.row[aria-pressed='true'] {
  border: 2px solid var(--brand);
  font-weight: 700;
  text-decoration: underline;
}
</style>
