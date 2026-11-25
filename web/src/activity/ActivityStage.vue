<script setup lang="ts">
import { computed } from 'vue';
import { surfaceLabel } from './rows';
import { labelText, maxItems, surfaces as surfaceNodes, type Particle, type StageView, type Surface } from './stage';

// The Live stream stage (#811, handoff §3.6): Project -> Surface -> goldie -> Served (#880) as SVG, a particle per
// request, with a live text summary beside it. The SVG is a picture (role="img"); the lanes are
// real buttons outside it, and pressing one asks the page to filter the feed to that Project.
// Everything shown is text, cut by labelText: a Project or a query is never markup.
const props = defineProps<{ view: StageView; filter?: string; surfaces?: string[]; missesOnly?: boolean }>();
const emit = defineEmits<{ filter: [project: string | undefined]; surface: [name: string]; item: [reference: string]; miss: [] }>();

const row = 44;
const rows = computed(() => Math.max(props.view.lanes.length, 5));
const height = computed(() => rows.value * row);
const laneY = (project: string) => {
  const i = props.view.lanes.findIndex((l) => l.project === project);
  return i < 0 ? height.value / 2 : i * row + row / 2;
};
// A Served label is cut short so that it clears goldie and the lane lines at 375 px (§7); the full text is the <title>.
const shortMax = 10;
const short = (s: string) => {
  const c = [...s];
  return c.length <= shortMax ? s : `${c.slice(0, shortMax - 1).join('')}…`;
};
const lerp = (a: number, b: number, t: number) => a + (b - a) * t;

// x is in percent of the width, y in pixels. Columns: Project 8, Surface 28, goldie 52, Served 92.
const xProject = 8;
const xSurface = 28;
const xGoldie = 52;
const xServed = 92;
const mid = computed(() => height.value / 2);
const surfaceY = (s: Surface) => {
  const i = surfaceNodes.indexOf(s as (typeof surfaceNodes)[number]);
  return i < 0 ? mid.value : (height.value * (i + 0.5)) / surfaceNodes.length;
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
// A node glows while a particle is passing it: the Surface at the middle of the first leg, goldie where the legs meet,
// the end node as the second leg arrives. The "+1" shows at an Item as a particle arrives. Without particles
// (prefers-reduced-motion) there is neither.
const near = 0.15;
// An Item that is not among the nodes has no node to glow.
const endNode = (p: Particle) => (p.end === 'miss' ? 'miss' : p.end === 'other' ? 'other' : itemNodes.value.find((n) => n.item === p.label)?.id);
const nodeAt = (p: Particle) => {
  if (p.leg === 'in') {
    if (p.surface !== 'other' && Math.abs(p.progress - 0.5) < near) return `surface:${p.surface}`;
    return p.progress > 1 - near ? 'goldie' : undefined;
  }
  if (p.progress < near) return 'goldie';
  return p.progress > 1 - near ? endNode(p) : undefined;
};
const glow = computed(() => new Set(props.view.particles.map(nodeAt)));
const arrivals = computed(() =>
  props.view.particles.filter((p) => p.leg === 'out' && p.end === 'item' && p.progress > 1 - near).map(endNode),
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
    <div class="nodes">
      <button
        v-for="n in surfaceNodes"
        :key="`b-${n}`"
        type="button"
        class="node-btn"
        :data-node="`surface:${n}`"
        :aria-label="n === 'hook' ? 'Other (hook)' : undefined"
        :aria-pressed="surfaces?.includes(n) ?? false"
        @click="emit('surface', n)"
      >
        {{ surfaceLabel[n === 'hook' ? 'other' : n] }}
      </button>
      <button v-for="n in itemNodes" :key="`b-${n.id}`" type="button" class="node-btn" :data-node="n.id" @click="emit('item', n.item)">
        {{ labelText(n.item) }}
      </button>
      <button type="button" class="node-btn miss-btn" data-node="miss" :aria-pressed="missesOnly ?? false" @click="emit('miss')">Miss</button>
    </div>
    <svg class="canvas" role="img" aria-label="Requests moving from Projects through their Surface and goldie to served Items and misses" :height="height">
      <line v-for="l in view.lanes" :key="`lp-${l.project}`" :x1="`${xProject}%`" :y1="laneY(l.project)" :x2="`${xGoldie}%`" :y2="mid" class="path lane-path" />
      <line v-for="s in surfaceNodes" :key="`sp-${s}`" :x1="`${xSurface}%`" :y1="surfaceY(s)" :x2="`${xGoldie}%`" :y2="mid" class="path" />
      <line v-for="n in itemNodes" :key="`ip-${n.id}`" :x1="`${xGoldie}%`" :y1="mid" :x2="`${xServed}%`" :y2="n.y" class="path" />
      <line :x1="`${xGoldie}%`" :y1="mid" :x2="`${xServed}%`" :y2="otherY" class="path" />
      <line :x1="`${xGoldie}%`" :y1="mid" :x2="`${xServed}%`" :y2="missY" class="path miss-path" />
      <g v-for="s in surfaceNodes" :key="s">
        <circle :cx="`${xSurface}%`" :cy="surfaceY(s)" r="8" class="node" :class="{ glow: glow.has(`surface:${s}`) }" :data-node="`surface:${s}`" />
        <text :x="`${xSurface}%`" :y="surfaceY(s) - 12" text-anchor="middle" class="tag">{{ s }}</text>
      </g>
      <circle :cx="`${xGoldie}%`" :cy="mid" r="12" class="node" :class="{ glow: glow.has('goldie') }" data-node="goldie" />
      <text :x="`${xGoldie}%`" :y="mid + 28" text-anchor="middle" class="tag">goldie</text>
      <g v-for="n in itemNodes" :key="n.id">
        <circle :cx="`${xServed}%`" :cy="n.y" r="8" class="node" :class="{ glow: glow.has(n.id) }" :data-node="n.id" />
        <text :x="`${xServed}%`" :y="n.y - 12" text-anchor="end" class="tag"><title>{{ n.item }}</title>{{ short(n.item) }} · {{ n.count }}</text>
        <text v-if="arrivals.includes(n.id)" :x="`${xServed}%`" :y="n.y + 24" text-anchor="end" class="plus" aria-hidden="true">+1</text>
      </g>
      <circle :cx="`${xServed}%`" :cy="otherY" r="8" class="node" :class="{ glow: glow.has('other') }" data-node="other" />
      <text :x="`${xServed}%`" :y="otherY - 12" text-anchor="end" class="tag"><title>other Items &amp; bundles</title>other · {{ view.otherItems }}</text>
      <circle :cx="`${xServed}%`" :cy="missY" r="8" class="node miss-node" :class="{ glow: glow.has('miss') }" data-node="miss" />
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
.nodes {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  grid-column: 1 / -1;
}
.node-btn {
  min-width: var(--target-min);
  min-height: var(--target-min);
  padding: 0 var(--space-3);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
  cursor: pointer;
  overflow-wrap: anywhere;
  unicode-bidi: isolate;
}
.node-btn[aria-pressed='true'] {
  border-width: 2px;
  border-color: var(--brand);
  background: var(--brand-tint);
  color: var(--brand-tint-ink);
  font-weight: 700;
}
.node-btn.miss-btn[aria-pressed='true'] {
  border-color: var(--miss-border);
  background: var(--miss-surface);
  color: var(--miss-ink);
}
.node.glow {
  fill: var(--brand-tint);
  filter: drop-shadow(0 0 6px var(--brand));
}
.plus {
  fill: var(--brand);
  font-size: var(--text-sm);
  font-weight: 700;
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
