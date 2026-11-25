<script setup lang="ts">
import { computed } from 'vue';
import { labelText, maxItems, surfaces, type Particle, type StageView, type Surface } from './stage';

// The Live stream stage (#811, handoff §3.6): Project -> Surface -> goldie -> Served (#880) as SVG, a particle per
// request, with a live text summary beside it. The SVG is a picture (role="img"); the lanes are
// real buttons outside it, and pressing one asks the page to filter the feed to that Project.
// Everything shown is text, cut by labelText: a Project or a query is never markup.
const props = defineProps<{ view: StageView; filter?: string }>();
const emit = defineEmits<{ filter: [project: string | undefined] }>();

const row = 44;
const rows = computed(() => Math.max(props.view.lanes.length, 5));
const height = computed(() => rows.value * row);
const laneY = (project: string) => {
  const i = props.view.lanes.findIndex((l) => l.project === project);
  return i < 0 ? height.value / 2 : i * row + row / 2;
};
const lerp = (a: number, b: number, t: number) => a + (b - a) * t;

// x is in percent of the width, y in pixels. Columns: Project 8, Surface 28, goldie 52, Served 92.
const xProject = 8;
const xSurface = 28;
const xGoldie = 52;
const xServed = 92;
const mid = computed(() => height.value / 2);
const surfaceY = (s: Surface) => {
  const i = surfaces.indexOf(s as (typeof surfaces)[number]);
  return i < 0 ? mid.value : (height.value * (i + 0.5)) / surfaces.length;
};
// The Served column has five slots: up to three Items, other Items & bundles, Miss.
const slotY = (i: number) => (height.value * (i + 0.5)) / (maxItems + 2);
const itemNodes = computed(() => props.view.items.map((it, i) => ({ ...it, id: `item:${it.item}`, y: slotY(i) })));
const otherY = computed(() => slotY(maxItems));
const missY = computed(() => slotY(maxItems + 1));
const endY = (p: Particle) =>
  p.end === 'miss' ? missY.value : p.end === 'other' ? otherY.value : slotY(Math.max(props.view.items.findIndex((i) => i.item === p.label), 0));

// A particle goes Project -> its Surface node (the middle of the first leg) -> goldie -> its end node.
function place(p: Particle) {
  const t = p.progress;
  const from = { x: xProject, y: laneY(p.project) };
  if (p.leg === 'in') {
    // A surface without a node (other) goes straight to goldie.
    if (p.surface === 'other') return { x: lerp(from.x, xGoldie, t), y: lerp(from.y, mid.value, t) };
    const via = { x: xSurface, y: surfaceY(p.surface) };
    if (t < 0.5) return { x: lerp(from.x, via.x, t * 2), y: lerp(from.y, via.y, t * 2) };
    return { x: lerp(via.x, xGoldie, t * 2 - 1), y: lerp(via.y, mid.value, t * 2 - 1) };
  }
  return { x: lerp(xGoldie, xServed, t), y: lerp(mid.value, endY(p), t) };
}
const dots = computed(() =>
  props.view.particles.map((p) => ({ ...p, ...place(p), cls: ['particle', `surface-${p.surface}`, { miss: p.target === 'miss' }] })),
);
const toggle = (project: string) => emit('filter', props.filter === project ? undefined : project);
</script>

<template>
  <section class="stage" aria-label="Live stream">
    <h2 class="stage-h">Live stream</h2>
    <div class="lanes">
      <button
        v-for="l in view.lanes"
        :key="l.project"
        type="button"
        class="lane"
        :aria-pressed="filter === l.project"
        @click="toggle(l.project)"
      >
        <span class="name">{{ labelText(l.project) }}</span> <span class="n">{{ l.count }}</span>
      </button>
      <p v-if="!view.lanes.length" class="idle">No Project has asked yet.</p>
    </div>
    <svg class="canvas" role="img" aria-label="Requests moving from Projects through their Surface and goldie to served Items and misses" :height="height">
      <line v-for="l in view.lanes" :key="`lp-${l.project}`" :x1="`${xProject}%`" :y1="laneY(l.project)" :x2="`${xGoldie}%`" :y2="mid" class="path lane-path" />
      <line v-for="s in surfaces" :key="`sp-${s}`" :x1="`${xSurface}%`" :y1="surfaceY(s)" :x2="`${xGoldie}%`" :y2="mid" class="path" />
      <line v-for="n in itemNodes" :key="`ip-${n.id}`" :x1="`${xGoldie}%`" :y1="mid" :x2="`${xServed}%`" :y2="n.y" class="path" />
      <line :x1="`${xGoldie}%`" :y1="mid" :x2="`${xServed}%`" :y2="otherY" class="path" />
      <line :x1="`${xGoldie}%`" :y1="mid" :x2="`${xServed}%`" :y2="missY" class="path miss-path" />
      <g v-for="s in surfaces" :key="s">
        <circle :cx="`${xSurface}%`" :cy="surfaceY(s)" r="8" class="node" :data-node="`surface:${s}`" />
        <text :x="`${xSurface}%`" :y="surfaceY(s) - 12" text-anchor="middle" class="tag">{{ s }}</text>
      </g>
      <circle :cx="`${xGoldie}%`" :cy="mid" r="12" class="node" data-node="goldie" />
      <text :x="`${xGoldie}%`" :y="mid + 28" text-anchor="middle" class="tag">goldie</text>
      <g v-for="n in itemNodes" :key="n.id">
        <circle :cx="`${xServed}%`" :cy="n.y" r="8" class="node" :data-node="n.id" />
        <text :x="`${xServed}%`" :y="n.y - 12" text-anchor="end" class="tag">{{ n.item }} · {{ n.count }}</text>
      </g>
      <circle :cx="`${xServed}%`" :cy="otherY" r="8" class="node" data-node="other" />
      <text :x="`${xServed}%`" :y="otherY - 12" text-anchor="end" class="tag">other Items &amp; bundles · {{ view.otherItems }}</text>
      <circle :cx="`${xServed}%`" :cy="missY" r="8" class="node miss-node" data-node="miss" />
      <text :x="`${xServed}%`" :y="missY + 22" text-anchor="end" class="tag">Miss</text>
      <circle v-for="(p, i) in dots" :key="i" :cx="`${p.x}%`" :cy="p.y" r="5" :class="p.cls">
        <title>{{ p.surface }} · {{ p.end === 'other' ? 'other Items & bundles' : p.label }}</title>
      </circle>
    </svg>
    <div class="side">
      <p role="status" class="chip">{{ view.chip }}</p>
      <p role="status" class="summary">{{ view.summary }}</p>
      <p class="legend">Colour is the surface: <span class="k mcp">mcp</span> <span class="k hook">hook</span> <span class="k cli">cli</span> <span class="k ui">ui</span> <span class="k other">other</span></p>
    </div>
  </section>
</template>

<style scoped>
.stage {
  display: grid;
  grid-template-columns: minmax(10rem, 16rem) minmax(0, 1fr);
  gap: var(--space-4);
  margin: 0 0 var(--space-4);
  padding: var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: radial-gradient(circle at center, var(--stream-stage), var(--surface-1));
}
.stage-h {
  grid-column: 1 / -1;
  margin: 0;
  font-size: var(--text-lg);
}
.lanes {
  display: flex;
  flex-direction: column;
}
.lane {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  min-height: var(--target-min);
  height: 44px;
  padding: 0 var(--space-3);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
  cursor: pointer;
}
.lane[aria-pressed='true'] {
  border-width: 2px;
  border-color: var(--brand);
  background: var(--brand-tint);
  color: var(--brand-tint-ink);
  font-weight: 700;
}
.lane .name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  unicode-bidi: isolate;
}
.lane .n {
  font-variant-numeric: tabular-nums;
}
.idle,
.chip,
.summary,
.legend {
  margin: 0;
  color: var(--fg-secondary);
}
.canvas {
  width: 100%;
}
.path {
  stroke: var(--stream-line);
  stroke-width: 2;
}
.miss-path {
  stroke: var(--miss-line);
  stroke-dasharray: 4 4;
}
.node {
  fill: var(--surface-1);
  stroke: var(--brand);
  stroke-width: 3;
}
.tag {
  fill: var(--fg-secondary);
  font-size: var(--text-sm);
}
.particle {
  fill: var(--fg-secondary);
}
.surface-mcp,
.k.mcp {
  color: var(--brand);
  fill: var(--brand);
}
.surface-hook,
.k.hook {
  color: var(--fg-secondary);
  fill: var(--fg-secondary);
}
.surface-cli,
.k.cli {
  color: var(--ok-ink);
  fill: var(--ok-ink);
}
.surface-ui,
.k.ui {
  color: var(--fg-default);
  fill: var(--fg-default);
}
.surface-other {
  fill: transparent;
  stroke: var(--fg-secondary);
  stroke-width: 2;
}
.k.other {
  color: var(--fg-secondary);
  text-decoration: underline dashed;
}
.particle.miss {
  stroke: var(--miss-solid);
  stroke-width: 2;
}
.k {
  font-weight: 700;
}
@media (max-width: 767px) {
  .stage {
    grid-template-columns: minmax(0, 1fr);
  }
}
.side {
  grid-column: 1 / -1;
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3) var(--space-6);
}
</style>
