<script setup lang="ts">
import { computed, ref } from 'vue';
import { RouterLink, useRoute } from 'vue-router';
import { useLoad } from '../api/useLoad';
import { listAgents, listLoops, listSkills, plain } from '../assets/api';
import CommandSnippet from '../assets/CommandSnippet.vue';
import { assetTabs } from '../assets/tabs';
import { listArticles } from '../articles/api';
import ScopeBadge from '../components/ScopeBadge.vue';
import { projectOf, projectQuery } from '../projects/api';

// Home without a query (handoff §3.2 items 2 to 4): the Browse tiles, the "Specific to" card and
// the agent checklist. The selected Project (`?project=`) rides on every tile link and count, as on
// the brand link in AppShell. Each count is its own load: a list that fails leaves its tile without
// a count and nothing else. The article list is read once, for the Articles tile and the card.
const route = useRoute();
const project = computed(() => projectOf(route.query));
const query = computed(() => projectQuery(route.query));
const key = computed(() => `home:${project.value}`);
const loads = {
  articles: useLoad(key, (_, signal) => listArticles(project.value, signal)),
  skills: useLoad(key, (_, signal) => listSkills(project.value, signal).then((l) => l.skills.length)),
  agents: useLoad(key, (_, signal) => listAgents(project.value, signal).then((l) => l.agents.length)),
  loops: useLoad(key, (_, signal) => listLoops(project.value, signal).then((l) => l.loops.length)),
};

const purposes = {
  articles: 'Guidance on how we build, scoped to your project.',
  skills: 'Playbooks your agents follow for a task.',
  agents: 'Subagents that take on a role.',
  loops: 'Recurring prompts that run on an interval.',
} as const;
type Slug = keyof typeof purposes;
const counts: Record<Slug, () => number | undefined> = {
  articles: () => loads.articles.data.value?.articles.length,
  skills: () => loads.skills.data.value,
  agents: () => loads.agents.data.value,
  loops: () => loads.loops.data.value,
};
const tiles = computed(() =>
  [
    { slug: 'articles' as Slug, name: 'Articles', to: { path: '/articles', query: query.value } },
    ...assetTabs
      .filter((t): t is (typeof assetTabs)[number] & { slug: Slug } => t.slug in loads)
      .map((t) => ({ slug: t.slug, name: t.name, to: { path: `/assets/${t.slug}`, query: query.value } })),
  ].map((t) => ({ ...t, purpose: purposes[t.slug], count: loads[t.slug].state.value === 'ready' ? counts[t.slug]() : undefined })),
);

// The articles that are specific to the Project: any Scope but global, overrides first, then by id
// (code-point order, as the server sorts).
const specific = computed(() =>
  (loads.articles.data.value?.articles ?? [])
    .filter((a) => a.scope !== 'global')
    .sort((a, b) => Number(!!b.overrides?.length) - Number(!!a.overrides?.length) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)),
);
// /items/article/<id>: the id is one encoded segment (ADR 0016).
const articleTo = (id: string) => ({ path: `/items/article/${encodeURIComponent(id)}`, query: query.value });

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
      <RouterLink v-for="t in tiles" :key="t.slug" :to="t.to" class="tile">
        <span class="name">{{ t.name }}</span>
        <span class="purpose">{{ t.purpose }}</span>
        <span v-if="t.count !== undefined" class="count">{{ t.count }}<span class="sr-only">{{ t.count === 1 ? ' item' : ' items' }}</span></span>
      </RouterLink>
    </div>
  </section>

  <section class="section" aria-labelledby="home-specific">
    <h2 id="home-specific">{{ project ? `Specific to ${plain(project)}` : 'Specific to your project' }}</h2>
    <p v-if="!project" class="empty">Pick a project to see the articles that only apply to it.</p>
    <template v-else-if="loads.articles.state.value === 'ready'">
      <p v-if="!specific.length" class="empty">Nothing specific to {{ plain(project) }} yet.</p>
      <ul v-else class="specific">
        <li v-for="a in specific" :key="a.id">
          <RouterLink :to="articleTo(a.id)">{{ plain(a.name) }}</RouterLink>
          <ScopeBadge :scope="plain(a.scope)" />
          <span v-for="s in a.overrides ?? []" :key="s" class="purpose">Overrides {{ plain(a.id) }} from {{ plain(s) }}</span>
        </li>
      </ul>
    </template>
  </section>

  <section class="section" aria-labelledby="home-agent">
    <h2 id="home-agent">Put your agent on the golden path</h2>
    <div class="steps">
      <div v-for="s in steps" :key="s.id" class="step">
        <div class="body">
          <span class="label">{{ s.label }}</span>
          <CommandSnippet :command="s.command" />
        </div>
        <button type="button" class="tick" :aria-pressed="ticked.includes(s.id)" :aria-label="`Done: ${s.label}`" @click="toggle(s.id)">
          <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true" focusable="false"><path d="M5 12l5 5 9-10" /></svg>
        </button>
      </div>
    </div>
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
.empty {
  margin: 0;
  color: var(--fg-secondary);
}
.specific {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}
.specific li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  overflow-wrap: anywhere;
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
