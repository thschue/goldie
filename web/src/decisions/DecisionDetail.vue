<script setup lang="ts">
import { computed } from 'vue';
import { errorSentence } from '../api/errorSentence';
import { useLoad } from '../api/useLoad';
import { plain } from '../assets/api';
import MarkdownBody from '../articles/MarkdownBody.vue';
import ScopeBadge from '../components/ScopeBadge.vue';
import { getDecision } from './api';

// The selected decision (handoff §3.8, #737): its Markdown body (through MarkdownBody, never raw
// HTML), the binding note of an accepted one, the facts the API serves and its history. Everything
// from a Source is untrusted and renders as text. The load key joins name and Project.
const props = defineProps<{ name: string; project: string; to: (name: string) => object }>();
const key = computed(() => `${props.name}\n${props.project}`);
const { state, data, failure, skeleton, retry } = useLoad(key, (k, signal) => {
  const i = k.indexOf('\n');
  return getDecision(k.slice(0, i), k.slice(i + 1), signal);
});

// A superseded_by is `decision:<name>`; it links only with that shape, anything else is plain text.
const successor = computed(() => {
  const m = /^decision:([A-Za-z0-9._-]+)$/.exec(data.value?.superseded_by ?? '');
  return m ? m[1] : '';
});
const chain = computed(() => data.value?.chain ?? []);
</script>

<template>
  <section class="detail" aria-label="Selected decision">
    <div v-if="skeleton" class="bar" aria-busy="true"></div>
    <div v-else-if="state === 'error'" class="box" role="alert">
      <p>{{ errorSentence(failure?.message ?? '', failure?.hint) }} {{ failure?.hint }}</p>
      <button v-if="failure?.status !== 404" type="button" class="retry" @click="retry">Retry</button>
    </div>
    <template v-else-if="state === 'ready' && data">
      <h2>{{ plain(data.title) }}</h2>
      <div class="meta">
        <span class="status" :class="data.status">{{ data.status }}</span>
        <ScopeBadge :scope="plain(data.scope)" />
        <time>{{ plain(data.date) }}</time>
      </div>
      <!-- Copy: handoff §8, the lifecycle notes. Plain text, not a live region. -->
      <p v-if="data.status === 'superseded'" class="note">
        Superseded — kept for history.
        <template v-if="successor">
          Agents follow <RouterLink :to="to(successor)">{{ plain(successor) }}</RouterLink> instead.
        </template>
      </p>
      <p v-else-if="data.status === 'proposed'" class="note">
        Proposed — under review. Agents and default API queries don't see it until it is accepted.
      </p>
      <p v-else-if="data.status === 'rejected'" class="note">
        Rejected — kept so the question isn't reopened without new facts. Not served to agents.
      </p>
      <p v-else-if="data.status === 'deprecated'" class="note">Deprecated — kept for history. Agents don't follow it.</p>
      <!-- Copy: handoff §8, "Binding note". -->
      <p v-if="data.status === 'accepted'" class="binding-note">
        <strong>Binding for agents.</strong> A change that contradicts this decision should stop and propose a new decision
        instead of silently deviating.
      </p>
      <MarkdownBody :source="data.content ?? ''" />
      <p v-if="data.keywords.length" class="keywords">
        <span v-for="k in data.keywords" :key="k" class="chip">{{ plain(k) }}</span>
      </p>
      <template v-if="chain.length > 1">
        <h3>History</h3>
        <ol class="history">
          <li v-for="c in chain" :key="c.name" :aria-current="c.name === data.name ? 'step' : undefined">
            <span v-if="c.name === data.name">{{ plain(c.title) }}</span>
            <RouterLink v-else :to="to(c.name)">{{ plain(c.title) }}</RouterLink>
            <span class="status" :class="c.status">{{ c.status }}</span>
          </li>
        </ol>
      </template>
    </template>
  </section>
</template>

<style scoped>
.detail {
  min-width: 0;
  padding: var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-1);
}
h2 {
  margin: 0 0 var(--space-2);
  font-size: var(--text-lg);
  overflow-wrap: anywhere;
}
h3 {
  margin: var(--space-6) 0 var(--space-2);
  font-size: var(--text-md);
}
.meta,
.keywords {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin: 0 0 var(--space-3);
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
.note {
  margin: 0 0 var(--space-3);
  color: var(--fg-secondary);
}
.binding-note {
  margin: 0 0 var(--space-4);
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--brand-tint-ink);
  border-radius: var(--radius-md);
  background: var(--brand-tint);
  color: var(--brand-tint-ink);
}
.status,
.chip {
  padding: 2px var(--space-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
}
.status.accepted {
  border-color: var(--brand-tint-ink);
  background: var(--brand-tint-ink);
  color: var(--surface-1);
}
.status.proposed {
  border-style: dashed;
}
.chip {
  border-color: var(--border-default);
  background: var(--surface-2);
}
.history {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding-left: var(--space-6);
}
.history li {
  overflow-wrap: anywhere;
}
.history li[aria-current='step'] {
  font-weight: 700;
}
.history a {
  display: inline-flex;
  align-items: center;
  min-height: var(--target-min);
}
.history .status {
  margin-left: var(--space-2);
  font-size: var(--text-sm);
  font-weight: 400;
}
.bar {
  min-height: 160px;
  background: var(--surface-3);
}
.box {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-4) var(--space-6);
  border: 1px solid var(--danger-border);
  border-radius: var(--radius-md);
  background: var(--danger-bg);
  color: var(--danger-ink);
}
.box p {
  margin: 0;
}
.retry {
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  border: 1px solid currentColor;
  border-radius: var(--radius-sm);
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
  font-weight: 600;
}
</style>
