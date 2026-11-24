<script setup lang="ts">
import { computed } from 'vue';
import { labelText, type Particle, type StageView } from './stage';

// The Live stream stage (#811, handoff §3.6): Project -> goldie -> Served as SVG, a particle per
// request, with a live text summary beside it. The SVG is a picture (role="img"); the lanes are
// real buttons outside it, and pressing one asks the page to filter the feed to that Project.
// Everything shown is text, cut by labelText: a Project or a query is never markup.
const props = defineProps<{ view: StageView; filter?: string }>();
const emit = defineEmits<{ filter: [project: string | undefined] }>();

const row = 44;
const rows = computed(() => Math.max(props.view.lanes.length, 3));
const height = computed(() => rows.value * row);
const laneY = (project: string) => {
  const i = props.view.lanes.findIndex((l) => l.project === project);
  return i < 0 ? height.value / 2 : i * row + row / 2;
};
const servedY = computed(() => height.value * 0.3);
const missY = computed(() => height.value * 0.75);
const lerp = (a: number, b: number, t: number) => a + (b - a) * t;

// x is in percent of the width, y in pixels: Project at 8, goldie at 50, Item or miss marker at 92.
function place(p: Particle) {
  const t = p.progress;
  if (p.leg === 'in') return { x: lerp(8, 50, t), y: lerp(laneY(p.project), height.value / 2, t) };
  return { x: lerp(50, 92, t), y: lerp(height.value / 2, p.target === 'miss' ? missY.value : servedY.value, t) };
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
    <svg class="canvas" role="img" aria-label="Requests moving from Projects through goldie to served Items and misses" :height="height">
      <line x1="8%" :y1="height / 2" x2="50%" :y2="height / 2" class="path" />
      <line x1="50%" :y1="height / 2" x2="92%" :y2="servedY" class="path" />
      <line x1="50%" :y1="height / 2" x2="92%" :y2="missY" class="path miss-path" />
      <circle cx="50%" :cy="height / 2" r="12" class="node" />
      <text x="50%" :y="height / 2 + 28" text-anchor="middle" class="tag">goldie</text>
      <text x="92%" :y="servedY - 10" text-anchor="end" class="tag">Served</text>
      <text x="92%" :y="missY + 20" text-anchor="end" class="tag">Miss</text>
      <circle v-for="(p, i) in dots" :key="i" :cx="`${p.x}%`" :cy="p.y" r="5" :class="p.cls">
        <title>{{ p.surface }} · {{ p.label }}</title>
      </circle>
    </svg>
    <div class="side">
      <p role="status" class="chip">{{ view.chip }}</p>
      <p role="status" class="summary">{{ view.summary }}</p>
      <p class="legend">Colour is the surface: <span class="k mcp">mcp</span> <span class="k cli">cli</span> <span class="k ui">ui</span> <span class="k other">other</span></p>
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
