<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { ApiError } from '../api/client';
import { session } from '../auth/session';
import { plain } from '../assets/api';
import { add, addText, agoText, announceEvery, canSearch, missBoard, itemText, msText, queryText, resultText, rowKey, shownText, surfaceLabel, surfaceOf, surfaces, timeText, traceLink, webLink, type Surface } from './rows';
import { projectOf } from '../projects/api';
import ActivityStage from './ActivityStage.vue';
import { createStage } from './stage';
import { createTiles, liveHeader } from './tiles';
import { streamActivity, type ActivityEvent, type Frame } from './stream';

// The activity feed on the merged stream of all replicas (handoff §3.6, §5.1, §5.3, §9; #630,
// #631). One table of the events, newest first, kept to the newest maxRows, a switcher that shows
// one replica's events, and a partial-view banner while a peer is down. New rows never take focus:
// nothing here calls focus(), and the rows are keyed by event id so a selection stays on its
// event. The miss count is a polite status that changes at most every 30 s. The header words how
// many replicas answered in the last minute, the tiles count the last five minutes from buckets
// (not from the rows), and the URL's `project` filters the rows (#809). The stage (#811) animates
// the live events only (not the history replayed on connect), pauses while the tab is hidden and
// draws no particle under prefers-reduced-motion; a lane filters the feed by the same `project`.
const route = useRoute();
const router = useRouter();
const project = computed(() => projectOf(route.query));
const setProject = (p?: string) => router.replace({ query: { ...route.query, project: p } });
const dropProject = () => router.replace({ query: { ...route.query, project: undefined } });
const rows = ref<ActivityEvent[]>([]);
const phase = ref<'connecting' | 'live' | 'reconnecting' | 'error'>('connecting');
// kept: a refusal on reconnect leaves the view that was live on screen, under the message.
const kept = ref(false);
const failed = ref<ApiError | Error>();
const dropped = ref(false);
const paused = ref(false);
const missesOnly = ref(false);
const selected = ref<string>();
// The replicas seen, by name: from their events (up) and from replica_status (up or down). `heard`
// is when each last answered (epoch ms): an event's time, or the status that said it is up; a
// replica reported down is not answering, so its status sets it to 0.
const fleet = ref(new Map<string, { up: boolean; lastSeen: string; heard: number }>());
let tiles = createTiles();
const fed = ref(0);
const only = ref<string>();
const picked = ref<Surface[]>([]);
const now = ref(Date.now());
const calm = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
let stage = createStage({ reduced: calm });
const stageView = ref(stage.read(Date.now()));
let frame = 0;
function drawStage() {
  frame = 0;
  stageView.value = stage.read(Date.now());
  if (stageView.value.particles.length && !document.hidden) frame = requestAnimationFrame(drawStage);
}
// Hidden: the stage drops its queue and stops drawing, so coming back is no burst.
function visibility() {
  stage.setHidden(document.hidden, Date.now());
  cancelAnimationFrame(frame);
  drawStage();
}
let held: ActivityEvent[] = [];
// Everything that arrived while paused, for the stage and the tiles (the rows keep only the newest
// maxRows); capped so a long pause cannot grow it without end.
const maxPending = 20_000;
let pending: ActivityEvent[] = [];
// The fresh part of it (live events, not replayed history): all the stage counts on Resume.
let pendingFresh: ActivityEvent[] = [];
// A reconnect while paused: the view is wiped on Resume, not under the reader during the pause.
let wipe = false;
let controller: AbortController | undefined;
let retry: ReturnType<typeof setTimeout> | undefined;
let attempts = 0;
const maxPause = 30_000;

const shown = computed(() =>
  rows.value.filter(
    (r) =>
      (!missesOnly.value || r.miss) &&
      (only.value === undefined || r.replica === only.value) &&
      (!project.value || r.project === project.value) &&
      (!picked.value.length || picked.value.includes(surfaceOf(r) as Surface)),
  ),
);
const header = computed(() =>
  paused.value ? 'Paused' : liveHeader(new Map([...fleet.value].map(([n, s]) => [n, s.heard])), now.value),
);
// fed changes whenever the tiles are fed, so the read happens again.
const last5 = computed(() => (fed.value, tiles.read(now.value)));
const pills = computed(() =>
  surfaces.map((s) => ({ s, label: surfaceLabel[s], count: rows.value.filter((r) => surfaceOf(r) === s).length })),
);
const pick = (s: Surface) => (picked.value = picked.value.includes(s) ? picked.value.filter((x) => x !== s) : [...picked.value, s]);
const board = computed(() => missBoard(rows.value));
const chosen = computed(() => rows.value.find((r) => rowKey(r) === selected.value));
// The links out (#808) come with the operator's Session (#807); a reader's has neither.
const grafana = computed(() => webLink(session.value?.grafana_url));
const traceHref = computed(() => traceLink(session.value?.trace_url_template, chosen.value?.trace_id));
// When narrow the panel sits under the feed: bring it into view on a selection, never moving focus
// (handoff §9), and without animation under prefers-reduced-motion.
const panel = ref<HTMLElement>();
watch(selected, async (key) => {
  if (key === undefined) return;
  await nextTick();
  const calm = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  panel.value?.scrollIntoView?.({ block: 'nearest', behavior: calm ? 'auto' : 'smooth' });
});
const missCount = computed(() => rows.value.filter((r) => r.miss).length);

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

// Frames of a reconnect wait here until the live marker arrives, then replace the view in one go.
let replayed: Frame[] = [];
function replay(frames: Frame[]) {
  replayed.push(...frames);
  if (!frames.some((f) => f.type === 'live')) return;
  const all = replayed;
  replayed = [];
  held = [];
  pending = [];
  pendingFresh = [];
  fleet.value = new Map();
  dropped.value = false;
  phase.value = 'connecting';
  attempts = 0;
  if (paused.value) wipe = true;
  else wipeView();
  apply(all);
}

function wipeView() {
  rows.value = [];
  tiles = createTiles();
  fed.value++;
  wipe = false;
}

function apply(frames: Frame[]) {
  const events: ActivityEvent[] = [];
  const fresh: ActivityEvent[] = [];
  for (const f of frames) {
    if (f.type === 'event') {
      events.push(f.event);
      if (phase.value === 'live') fresh.push(f.event);
      const seen = fleet.value.get(f.event.replica);
      const t = Math.min(Date.parse(f.event.time) || 0, Date.now());
      if (f.event.replica && !seen) fleet.value.set(f.event.replica, { up: true, lastSeen: '', heard: t });
      else if (seen) seen.heard = Math.max(seen.heard, t);
    } else if (f.type === 'status') {
      fleet.value.set(f.replica, { up: f.up, lastSeen: f.lastSeen, heard: f.up ? Date.now() : 0 });
      if (!paused.value) now.value = Date.now();
    } else if (f.type === 'dropped') dropped.value = true;
    else if (phase.value === 'connecting') phase.value = 'live';
  }
  if (!events.length) return;
  // Paused (WCAG 2.2.2): the stage, the tiles and the rows all stand still; Resume applies what was held.
  if (paused.value) {
    held = add(held, events);
    pending = [...pending, ...events].slice(-maxPending);
    pendingFresh = [...pendingFresh, ...fresh].slice(-maxPending);
    return;
  }
  now.value = Date.now();
  if (fresh.length) {
    stage.add(fresh, now.value);
    if (!frame) drawStage();
  }
  tiles.add(events, now.value);
  fed.value++;
  rows.value = add(rows.value, events);
}

// connect starts from an empty view (first load, Retry). After a drop, run() keeps the view and asks
// again after a growing pause (1 s, 2 s, 4 s ... 30 s, with jitter) that starts over once the live
// marker has arrived.
async function connect() {
  clearTimeout(retry);
  kept.value = false;
  attempts = 0;
  rows.value = [];
  held = [];
  tiles = createTiles();
  fed.value++;
  pending = [];
  pendingFresh = [];
  wipe = false;
  stage = createStage({ reduced: calm });
  stage.setHidden(document.hidden, Date.now());
  drawStage();
  fleet.value = new Map();
  only.value = undefined;
  selected.value = undefined;
  dropped.value = false;
  failed.value = undefined;
  phase.value = 'connecting';
  await run();
}

async function run() {
  controller?.abort();
  const mine = (controller = new AbortController());
  replayed = [];
  const onFrames = (frames: Frame[]) => {
    if (mine.signal.aborted) return;
    if (phase.value === 'reconnecting') replay(frames);
    else apply(frames);
  };
  try {
    await streamActivity(mine.signal, onFrames);
    if (!mine.signal.aborted) throw new Error('The stream ended.');
  } catch (e) {
    if (mine.signal.aborted) return;
    const refused = e instanceof ApiError && [401, 403, 503].includes(e.status);
    if (!refused && phase.value !== 'connecting') {
      phase.value = 'reconnecting';
      retry = setTimeout(run, Math.min(maxPause, 1000 * 2 ** attempts++) * (0.75 + Math.random() * 0.25));
      return;
    }
    kept.value = rows.value.length > 0 || fleet.value.size > 0;
    failed.value = e instanceof ApiError ? e : new Error("goldie isn't answering right now.");
    phase.value = 'error';
  }
}

function togglePause() {
  paused.value = !paused.value;
  if (!paused.value) {
    if (wipe) wipeView();
    rows.value = add(rows.value, [...held].reverse());
    held = [];
    // Counted, not animated: Resume is no burst of particles.
    stage.count(pendingFresh);
    tiles.add(pending, Date.now());
    fed.value++;
    pending = [];
    pendingFresh = [];
    now.value = Date.now();
    drawStage();
  }
}

const select = (key: string) => (selected.value = selected.value === key ? undefined : key);

// The announced miss count: set when the history ends, then at most once per announceEvery.
const announced = ref(0);
let announcedAt = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
// "stopped answering 2 minutes ago" ages while the page is open.
// Paused, the clock stands still too: the tiles' five-minute window would slide otherwise.
const tick = setInterval(() => !paused.value && (now.value = Date.now()), 30_000);
// The stage's summary changes at most every 30 s, whether or not a particle is moving.
const stageTick = setInterval(() => !frame && drawStage(), 5_000);
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

onMounted(() => {
  document.addEventListener('visibilitychange', visibility);
  connect();
});
onBeforeUnmount(() => {
  controller?.abort();
  clearTimeout(retry);
  clearTimeout(timer);
  clearInterval(tick);
  clearInterval(stageTick);
  cancelAnimationFrame(frame);
  document.removeEventListener('visibilitychange', visibility);
});

const status = computed(() => (failed.value instanceof ApiError ? failed.value.status : 0));
const hint = computed(() => (failed.value instanceof ApiError ? plain(failed.value.hint) : ''));
</script>

<template>
  <h1>Activity</h1>

  <p v-if="status === 403" class="box" role="alert">This page is for operators.</p>
  <p v-else-if="status === 401" class="box" role="alert">Sign in to see what is being asked of goldie.</p>
  <div v-else-if="phase === 'error'" class="box danger" role="alert">
    <p v-if="status === 429">Too many requests. Try again in a moment.</p>
    <p v-else-if="status === 503">Couldn't open the activity feed. {{ hint }}</p>
    <p v-else>Couldn't read the activity feed. {{ hint || 'Check your connection, then try again.' }}</p>
    <button type="button" class="control" @click="connect">Retry</button>
  </div>

  <template v-if="phase !== 'error' || kept">
    <div class="bar">
      <p class="label">
        <span v-if="phase === 'reconnecting'" class="dot idle" aria-hidden="true"></span>
        <!-- Always present, so the words reach a screen reader when they appear. -->
        <span role="status">{{ phase === 'reconnecting' ? 'Reconnecting…' : '' }}</span>
        <template v-if="phase === 'connecting'">Connecting to the replicas…</template>
        <template v-else-if="phase === 'error'">Not live</template>
        <template v-else-if="phase === 'live'">{{ header }}</template>
      </p>
      <button type="button" class="control" @click="togglePause">{{ paused ? 'Resume' : 'Pause' }}</button>
      <p role="status" class="misses">{{ announced }} {{ announced === 1 ? 'miss' : 'misses' }} in this feed</p>
      <a v-if="grafana" class="action" :href="grafana" target="_blank" rel="noopener noreferrer">Trends in Grafana ↗</a>
    </div>

    <ActivityStage :view="stageView" :filter="project || undefined" @filter="setProject" />

    <section class="tiles" aria-label="Last five minutes">
      <h2>Last five minutes</h2>
      <ul>
        <li>
          <span class="k">Requests</span>{{ ' ' }}
          <span class="v">{{ Math.round(last5.perMinute) }}<small> per minute</small></span>
        </li>
        <li>
          <span class="k">Misses</span>{{ ' ' }}
          <span class="v">{{ last5.missPercent === undefined ? '—' : `${last5.missPercent} %` }}</span>
        </li>
        <li>
          <span class="k">Projects asking</span>{{ ' ' }}
          <span class="v">{{ last5.projects }}</span>
        </li>
        <li>
          <span class="k">Busiest surface</span>{{ ' ' }}
          <span class="v">{{ last5.busiest ? surfaceLabel[last5.busiest] : '—' }}</span>
        </li>
        <li>
          <span class="k">Latency</span>{{ ' ' }}
          <span v-if="last5.latency" class="v">
            <small>p50</small> {{ msText(last5.latency.p50) }} <small>p95</small> {{ msText(last5.latency.p95) }}
          </span>
          <span v-else class="v">—</span>
        </li>
      </ul>
    </section>

    <div role="status">
      <div v-if="down.length" class="box warning">
        <strong>Partial view.</strong>
        <template v-for="d in down" :key="d.name">
          {{ plain(d.name) }} stopped answering {{ d.ago }} — its newer events are missing until it's back.
        </template>
      </div>
    </div>

    <div role="status">
      <p v-if="dropped" class="box warning">Some events were missed because this page fell behind.</p>
    </div>

    <section v-if="board.length" class="board" aria-labelledby="board-h">
      <h2 id="board-h">What people looked for and didn't find</h2>
      <ul>
        <li v-for="g in board" :key="g.query">
          <button type="button" class="control card" @click="selected = rowKey(g.latest)">
            <span class="q">{{ plain(g.query) }}</span>
            <span class="n">×{{ g.count }}</span>
            <span class="meta">
              {{ g.projects }} {{ g.projects === 1 ? 'project' : 'projects' }} · last seen {{ timeText(g.latest.time) }}
            </span>
          </button>
        </li>
      </ul>
    </section>

    <h2 class="feed-h">Feed</h2>
    <div class="toolbar">
      <div class="switcher" role="group" aria-label="Surface">
        <!-- Checkboxes, not pressed buttons: the replica switcher's buttons are the page's aria-pressed set. -->
        <button
          v-for="p in pills"
          :key="p.s"
          type="button"
          role="checkbox"
          class="control"
          :aria-checked="picked.includes(p.s)"
          @click="pick(p.s)"
        >
          <span v-if="picked.includes(p.s)" aria-hidden="true">✓ </span>{{ p.label }} <span class="count">{{ p.count }}</span>
        </button>
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
      <p v-if="project" class="chips">
        <button type="button" class="control" :aria-label="`Remove Project filter ${plain(project)}`" @click="dropProject">
          {{ plain(project) }} <span aria-hidden="true">✕</span>
        </button>
      </p>
      <!-- A checkbox, not a pressed button: the switcher's buttons are the page's aria-pressed set. -->
        <button type="button" role="checkbox" class="control miss-toggle" :aria-checked="missesOnly" @click="missesOnly = !missesOnly">
          <span v-if="missesOnly" aria-hidden="true">✓ </span>Misses only
        </button>
    </div>

    <p v-if="phase === 'live' && !rows.length" class="box">
      No requests yet. Run <code>goldie search</code> or ask your agent something.
    </p>
    <p v-else-if="phase === 'live' && !shown.length" class="box">
      Nothing matches these filters in the last five minutes.
    </p>
    <div v-if="shown.length || chosen" class="feed">
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
                <span v-if="e.late" class="late" title="Arrived after the reorder window">LATE</span>
              </td>
              <td>{{ e.kind }}</td>
              <td :id="`q-${i}`" class="query">{{ queryText(e) }}</td>
              <td :class="{ result: e.miss }" class="wrap">{{ itemText(e) }}</td>
              <td>
                <template v-if="surfaceOf(e)">{{ surfaceLabel[surfaceOf(e)!] }}</template>
                <template v-else><span aria-hidden="true">—</span><span class="sr-only">Not recorded</span></template>
              </td>
              <td class="wrap">{{ plain(e.project) || '—' }}</td>
              <td class="wrap">{{ plain(e.replica) || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <section v-if="chosen" ref="panel" class="detail" aria-label="Event detail">
        <h2>{{ chosen.miss ? 'Miss' : 'Served' }}</h2>
        <dl>
          <dt>Query</dt>
          <dd class="query">{{ queryText(chosen) }}</dd>
          <template v-if="chosen.served">
            <dt>Query → Item</dt>
            <dd class="query">{{ queryText(chosen) }} → {{ shownText(chosen.served) }}</dd>
          </template>
          <dt>Kind</dt>
          <dd>{{ chosen.kind }}</dd>
          <template v-if="chosen.project">
            <dt>Project</dt>
            <dd class="wrap">{{ plain(chosen.project) }}</dd>
          </template>
          <template v-if="chosen.latency_us">
            <dt>Latency</dt>
            <dd>{{ msText(chosen.latency_us) }}</dd>
          </template>
          <template v-if="surfaceOf(chosen)">
            <dt>Via</dt>
            <dd>{{ surfaceLabel[surfaceOf(chosen)!] }}</dd>
          </template>
          <template v-if="chosen.trace_id">
            <dt>Trace id</dt>
            <dd class="wrap">{{ shownText(chosen.trace_id) }}</dd>
          </template>
          <template v-if="chosen.token">
            <dt>Token</dt>
            <dd class="wrap">{{ plain(chosen.token) }}</dd>
          </template>
          <template v-if="chosen.replica">
            <dt>Replica</dt>
            <dd class="wrap">{{ plain(chosen.replica) }}</dd>
          </template>
        </dl>
        <p v-if="chosen.miss">{{ resultText(chosen) }}</p>
        <p v-if="chosen.miss" class="actions">
          <!-- The API gives no Source URL, so this goes to the Sources page, not to the repo. -->
          <RouterLink class="action" to="/ops/sources">{{ addText(chosen) }}</RouterLink>
        </p>
        <p v-if="canSearch(chosen)" class="actions">
          <RouterLink class="action" :to="{ path: '/', query: { q: queryText(chosen) } }">Try this search</RouterLink>
        </p>
        <p v-if="traceHref" class="actions">
          <a class="action" :href="traceHref" target="_blank" rel="noopener noreferrer">Open trace ↗</a>
        </p>
      </section>
    </div>
  </template>
</template>

<style scoped>
.tiles ul {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
  margin: 0 0 var(--space-4);
  padding: 0;
  list-style: none;
}
.tiles li {
  display: flex;
  flex-direction: column;
  min-width: 10rem;
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-2);
}
.tiles .k {
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
.tiles .v {
  font-size: var(--text-lg);
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.tiles small {
  font-size: var(--text-sm);
  font-weight: 400;
}
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
}
.toolbar .switcher,
.toolbar .chips {
  margin: 0;
}
.feed-h,
.tiles h2 {
  margin: 0 0 var(--space-3);
  font-size: var(--text-lg);
}
.board ul {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
  margin: 0 0 var(--space-4);
  padding: 0;
  list-style: none;
}
.card {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-1);
  padding: var(--space-3) var(--space-4);
  border-color: var(--miss-border);
  background: var(--miss-surface);
  color: var(--miss-ink);
  text-align: left;
}
.card .q {
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
  unicode-bidi: isolate;
}
.card .meta {
  font-size: var(--text-sm);
  font-weight: 400;
}
.feed {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: var(--space-4);
}
.feed .scroll {
  /* 800 px beside the detail (handoff §7); where that does not fit, the detail wraps under it. */
  flex: 1 1 800px;
  min-width: 0;
}
.detail {
  flex: 1 1 280px;
  min-width: 0;
  padding: var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-2);
}
.detail h2 {
  margin-top: 0;
}
.detail dt {
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
.detail dd {
  margin: 0 0 var(--space-3);
}
.actions {
  margin: 0 0 var(--space-3);
}
.action {
  display: inline-flex;
  align-items: center;
  min-height: var(--target-min);
  color: var(--brand);
  font-weight: 600;
  text-decoration: underline;
}
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
.dot.idle {
  color: var(--fg-secondary);
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
  border-color: var(--brand);
  background: var(--brand-tint);
  color: var(--brand-tint-ink);
}
/* Gold is for Miss (handoff §6.2 to §6.4): only "Misses only" is gold when checked. */
.control.miss-toggle[aria-checked='true'] {
  border-color: var(--miss-solid);
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
