<script setup lang="ts">
import { ref } from 'vue';
import { RouterLink } from 'vue-router';
import { useLoad } from '../api/useLoad';
import { listAgents, listLoops, listSkills } from '../assets/api';
import CommandSnippet from '../assets/CommandSnippet.vue';
import { assetTabs } from '../assets/tabs';

// Home without a query (handoff §3.2 items 2 and 4): the Browse tiles and the agent checklist.
// Each count is its own load: a list that fails leaves its tile without a count and nothing else.
const key = ref('home');
const loads = {
  skills: useLoad(key, (_, signal) => listSkills('', signal).then((l) => l.skills.length)),
  agents: useLoad(key, (_, signal) => listAgents('', signal).then((l) => l.agents.length)),
  loops: useLoad(key, (_, signal) => listLoops('', signal).then((l) => l.loops.length)),
};

const purposes = {
  skills: 'Playbooks your agents follow for a task.',
  agents: 'Subagents that take on a role.',
  loops: 'Recurring prompts that run on an interval.',
} as const;
const tiles = assetTabs
  .filter((t): t is (typeof assetTabs)[number] & { slug: keyof typeof loads } => t.slug in loads)
  .map((t) => ({ ...t, purpose: purposes[t.slug], load: loads[t.slug] }));

// The MCP endpoint is this server's origin plus /mcp.
const mcpUrl = `${typeof location === 'undefined' ? '' : location.origin}/mcp`;
const steps = [
  { id: 'init', label: 'Set up this repository', command: 'goldie init' },
  { id: 'mcp', label: 'Connect your agent to goldie', command: `claude mcp add --transport http goldie ${mcpUrl}` },
  { id: 'apply', label: 'Write the instruction block', command: 'goldie instructions apply' },
];

// Ticks are a convenience kept per browser. Storage that is missing or throws is never an error.
const storageKey = 'goldie.home.checklist';
function readTicks(): string[] {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(storageKey) ?? '[]');
    return Array.isArray(v) ? v.filter((s): s is string => typeof s === 'string') : [];
  } catch {
    return [];
  }
}
const ticked = ref(readTicks());
function toggle(id: string) {
  ticked.value = ticked.value.includes(id) ? ticked.value.filter((s) => s !== id) : [...ticked.value, id];
  try {
    localStorage.setItem(storageKey, JSON.stringify(ticked.value));
  } catch {
    // Kept for this visit only.
  }
}
</script>

<template>
  <section class="section" aria-labelledby="home-browse">
    <h2 id="home-browse">Browse the golden path</h2>
    <div class="tiles">
      <RouterLink v-for="t in tiles" :key="t.slug" :to="`/assets/${t.slug}`" class="tile">
        <span class="name">{{ t.name }}</span>
        <span class="purpose">{{ t.purpose }}</span>
        <span v-if="t.load.state.value === 'ready'" class="count">{{ t.load.data.value }}</span>
      </RouterLink>
    </div>
  </section>

  <section class="section" aria-labelledby="home-agent">
    <h2 id="home-agent">Put your agent on the golden path</h2>
    <ol class="steps">
      <li v-for="s in steps" :key="s.id" class="step">
        <div class="body">
          <span class="label">{{ s.label }}</span>
          <CommandSnippet :command="s.command" />
        </div>
        <button type="button" class="tick" :aria-pressed="ticked.includes(s.id)" :aria-label="`Done: ${s.label}`" @click="toggle(s.id)">
          <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true" focusable="false"><path d="M5 12l5 5 9-10" /></svg>
        </button>
      </li>
    </ol>
  </section>
</template>

<style scoped>
.section {
  max-width: 940px;
  margin-bottom: var(--space-6);
}
.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: var(--space-3);
}
.tile {
  display: grid;
  gap: var(--space-2);
  min-height: var(--target-min);
  padding: var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-1);
  color: var(--fg-default);
  text-decoration: none;
}
.tile:hover {
  background: var(--surface-2);
  border-color: var(--border-strong);
}
.name {
  font-weight: 600;
}
.purpose {
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
.count {
  font-size: var(--text-lg, 1.25rem);
  font-weight: 600;
}
.steps {
  display: grid;
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}
.step {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-1);
}
.body {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
}
.label {
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
.tick {
  flex: none;
  display: inline-grid;
  place-items: center;
  width: var(--target-min);
  height: var(--target-min);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface-1);
  color: transparent;
  cursor: pointer;
}
.tick svg {
  fill: none;
  stroke: currentColor;
  stroke-width: 2.5;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.tick[aria-pressed='true'] {
  border-color: var(--brand);
  background: var(--brand-tint);
  color: var(--brand-tint-ink);
}
</style>
