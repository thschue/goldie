<script setup lang="ts">
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { carried } from '../projects/api';
import type { SearchHit } from './api';

// One search hit (handoff §3.3, §4 ResultCard): the whole card is one link to
// the Item, so everything inside it is plain text in spans. Name and matched
// text come from a Source and are untrusted: interpolated, never rendered as
// markup. `enforced` and `overrides` may be absent (an older server): absent
// means unknown, so it shows no chip and no line (#529).
const props = defineProps<{ hit: SearchHit }>();

// /items/<kind>/<id>, each segment percent-encoded: an article id such as
// platform/deploy travels as one segment (platform%2Fdeploy) and the route
// /items/:kind/:name decodes it back.
// The link carries the search it comes from (q, kind) and the Project, for the Item's back link.
const route = useRoute();
const to = computed(() => ({
  path: `/items/${encodeURIComponent(props.hit.kind)}/${encodeURIComponent(props.hit.id)}`,
  query: carried(route.query, ['q', 'kind', 'project']),
}));

// The score bar is decoration: the score stays text. Scores run about 1 to 10
// (exact keyword +4, name word +2, substring +1), so the bar is score x 10 %,
// capped, with a floor so a low score still shows.
const bar = computed(() => `${Math.min(100, Math.max(4, props.hit.score * 10))}%`);
// SearchHit carries no summary today; show one only if a server sends it (handoff §3.3).
const summary = computed(() => {
  const s = (props.hit as { summary?: unknown }).summary;
  return typeof s === 'string' ? s : '';
});
</script>

<template>
  <li class="hit">
    <RouterLink :to="to" class="link">
      <!-- The name is a bare text node of the head, not an element of its own:
           the enforced chip stays the only element in the card whose text is
           "enforced", even for an Item whose name holds the word. -->
      <span class="head">
        <span class="badge kind">{{ hit.kind }}</span>
        {{ hit.name }}
        <span class="badge scope">{{ hit.scope }}</span>
        <span v-if="hit.enforced === true" class="badge enforced">
          <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true" focusable="false">
            <path d="M12 3l8 3v6c0 4.5-3.2 8-8 9-4.8-1-8-4.5-8-9V6z" />
          </svg>
          enforced
        </span>
        <span v-if="hit.status === 'draft'" class="badge draft">draft</span>
        <span v-else-if="hit.status === 'deprecated'" class="badge deprecated">
          deprecated<template v-if="hit.replaced_by"> → {{ hit.replaced_by }}</template>
        </span>
      </span>
      <span class="matched">Matched: {{ hit.matched.join(', ') }}</span>
      <!-- One line per shadowed Scope. The API's overrides holds Scopes, not
           Items: the shadowed Item is this hit's own kind and id. -->
      <span v-for="scope in hit.overrides ?? []" :key="scope" class="overrides">
        Overrides {{ hit.kind }}:{{ hit.id }} from {{ scope }}
      </span>
      <span v-if="summary" class="summary">{{ summary }}</span>
      <span class="score">
        <span class="bar" aria-hidden="true"><span class="fill" :style="{ width: bar }"></span></span>
        <span>score {{ hit.score }}</span>
      </span>
    </RouterLink>
  </li>
</template>

<style scoped>
.hit {
  list-style: none;
}
.link {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-height: var(--target-min);
  padding: var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-1);
  color: var(--fg-default);
  text-decoration: none;
}
.link:hover {
  background: var(--surface-2);
  border-color: var(--border-strong);
}
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  font-weight: 600;
  overflow-wrap: anywhere;
}
.badge {
  font-weight: 400;
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 2px var(--space-2);
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
}
.kind {
  background: var(--brand-tint);
  color: var(--brand-tint-ink);
  font-family: var(--font-mono);
}
.scope {
  border: 1px solid var(--border-strong);
  color: var(--fg-secondary);
}
.enforced {
  background: var(--ok-bg);
  color: var(--ok-ink);
  font-weight: 600;
}
.draft {
  border: 1px dashed var(--border-strong);
  color: var(--fg-secondary);
}
.deprecated {
  background: var(--miss-bg);
  color: var(--miss-ink);
  font-weight: 600;
}
.summary {
  color: var(--fg-default);
  overflow-wrap: anywhere;
}
.score {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
.bar {
  display: block;
  flex: 0 1 120px;
  height: 6px;
  border-radius: 3px;
  background: var(--surface-3);
}
.fill {
  display: block;
  height: 100%;
  border-radius: 3px;
  background: var(--brand);
}
.enforced svg {
  fill: none;
  stroke: currentColor;
  stroke-width: 2;
  stroke-linejoin: round;
}
.matched,
.overrides {
  color: var(--fg-secondary);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}
.overrides {
  color: var(--fg-secondary);
  font-weight: 600;
}
</style>
