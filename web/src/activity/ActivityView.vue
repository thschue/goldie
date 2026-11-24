<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { ApiError } from '../api/client';
import { plain } from '../assets/api';
import { add, agoText, announceEvery, queryText, resultText, rowKey, timeText } from './rows';
import { streamActivity, type ActivityEvent, type Frame } from './stream';

// The activity feed on the merged stream of all replicas (handoff §3.6, §5.1, §5.3, §9; #630,
// #631). One table of the events, newest first, kept to the newest maxRows, a switcher that shows
// one replica's events, and a partial-view banner while a peer is down. New rows never take focus:
// nothing here calls focus(), and the rows are keyed by event id so a selection stays on its
// event. The miss count is a polite status that changes at most every 30 s. Out of scope here:
// the Miss board and detail panel, and the live stage.
const rows = ref<ActivityEvent[]>([]);
const phase = ref<'connecting' | 'live' | 'error'>('connecting');
const failed = ref<ApiError | Error>();
const dropped = ref(false);
const paused = ref(false);
const missesOnly = ref(false);
const selected = ref<string>();
// The replicas seen, by name: from their events (up) and from replica_status (up or down).
const fleet = ref(new Map<string, { up: boolean; lastSeen: string }>());
const only = ref<string>();
const now = ref(Date.now());
let held: ActivityEvent[] = [];
let controller: AbortController | undefined;

const shown = computed(() =>
  rows.value.filter((r) => (!missesOnly.value || r.miss) && (only.value === undefined || r.replica === only.value)),
);
const missCount = computed(() => rows.value.filter((r) => r.miss).length);
const replica = computed(() => plain(rows.value[0]?.replica));

const replicas = computed(() =>
  [...fleet.value]
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
    .map(([name, s]) => ({ name, up: s.up, count: rows.value.filter((r) => r.replica === name).length })),
);
const peers = computed(() => replicas.value.length > 1);
const down = computed(() =>
  [...fleet.value].filter(([, s]) => !s.up).map(([name, s]) => ({ name, ago: agoText(s.lastSeen, now.value) })),
);
const choose = (name?: string) => (only.value = name);

function apply(frames: Frame[]) {
  const events: ActivityEvent[] = [];
  for (const f of frames) {
    if (f.type === 'event') {
      events.push(f.event);
      if (f.event.replica && !fleet.value.has(f.event.replica)) fleet.value.set(f.event.replica, { up: true, lastSeen: '' });
    } else if (f.type === 'status') {
      fleet.value.set(f.replica, { up: f.up, lastSeen: f.lastSeen });
      now.value = Date.now();
    } else if (f.type === 'dropped') dropped.value = true;
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
  fleet.value = new Map();
  only.value = undefined;
  selected.value = undefined;
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

const select = (key: string) => (selected.value = selected.value === key ? undefined : key);

// The announced miss count: set when the history ends, then at most once per announceEvery.
const announced = ref(0);
let announcedAt = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
// "stopped answering 2 minutes ago" ages while the page is open.
const tick = setInterval(() => (now.value = Date.now()), 30_000);
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
  clearInterval(tick);
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
        <template v-if="phase === 'connecting'">Connecting to the replicas…</template>
        <template v-else-if="peers">
          Live · merged from all replicas ({{ replicas.filter((r) => r.up).length }} of {{ replicas.length }} answering)
        </template>
        <template v-else>
          Live · replica<template v-if="replica"> <strong>{{ replica }}</strong></template>
        </template>
      </p>
      <button type="button" class="control" @click="togglePause">{{ paused ? 'Resume' : 'Pause' }}</button>
      <!-- A checkbox, not a pressed button: the switcher's buttons are the page's aria-pressed set. -->
      <button type="button" role="checkbox" class="control" :aria-checked="missesOnly" @click="missesOnly = !missesOnly">
        <span v-if="missesOnly" aria-hidden="true">✓ </span>Misses only
      </button>
      <p role="status" class="misses">{{ announced }} {{ announced === 1 ? 'miss' : 'misses' }} in this feed</p>
    </div>

    <div v-if="peers" class="switcher" role="group" aria-label="Replicas">
      <button type="button" class="control" :aria-pressed="only === undefined" @click="choose()">
        All replicas <span class="count">{{ rows.length }}</span>
      </button>
      <button
        v-for="r in replicas"
        :key="r.name"
        type="button"
        class="control"
        :aria-pressed="only === r.name"
        @click="choose(r.name)"
      >
        <span class="dot" :class="{ off: !r.up }" aria-hidden="true"></span>{{ plain(r.name) }}
        <span class="state">{{ r.up ? 'up' : 'down' }}</span> <span class="count">{{ r.count }}</span>
      </button>
    </div>

    <div v-if="down.length" class="box warning">
      <strong>Partial view.</strong>
      <template v-for="d in down" :key="d.name">
        {{ plain(d.name) }} stopped answering {{ d.ago }} — its newer events are missing until it's back.
      </template>
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
          <tr v-for="(e, i) in shown" :key="rowKey(e)" :class="{ miss: e.miss, selected: selected === rowKey(e) }">
            <td>
              <button type="button" class="row" :aria-pressed="selected === rowKey(e)" :aria-describedby="`q-${i}`" @click="select(rowKey(e))">
                {{ timeText(e.time) }}
              </button>
              <span v-if="e.late" class="late" title="This event reached the merge after its reorder window">LATE</span>
            </td>
            <td>{{ e.kind }}</td>
            <td :id="`q-${i}`" class="query">{{ queryText(e) }}</td>
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
.switcher {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
  margin-bottom: var(--space-4);
}
.count {
  font-variant-numeric: tabular-nums;
}
.dot {
  display: inline-block;
  width: 0.6em;
  height: 0.6em;
  margin-right: var(--space-2);
  border-radius: 50%;
  background: currentColor;
}
.dot.off {
  background: transparent;
  box-shadow: inset 0 0 0 2px currentColor;
}
.late {
  display: block;
  font-size: var(--text-sm);
  font-weight: 700;
  color: var(--miss-ink);
}
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
.control[aria-pressed='true'],
.control[aria-checked='true'] {
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
