<script setup lang="ts">
import { computed } from 'vue';
import { useLoad } from '../api/useLoad';
import { role, session } from '../auth/session';
import { webLink } from '../activity/rows';
import { getArticleUsage } from './api';

// The Usage card (#901, handoff §3.4): the uses of one article on this replica since the process
// start, per client, as text and never a chart. Readers and operators see it, with no identity
// (§5.3). A failed or malformed read is one line inside the card, never a page error. The Grafana
// link is the operator's, and only with a grafana_url on the Session (§8).
const props = defineProps<{ id: string; project: string }>();
const key = computed(() => props.id);
const { state, data, skeleton } = useLoad(key, (id, signal) => getArticleUsage(id, props.project, signal));

const clients = ['cli', 'mcp', 'other'] as const;
// A fixed UTC form, so the label reads the same for every reader.
const since = computed(() => {
  if (!data.value) return '';
  const d = new Date(data.value.since);
  const month = d.toLocaleString('en-GB', { month: 'short', timeZone: 'UTC' });
  return `${d.getUTCDate()} ${month} ${d.getUTCFullYear()} ${d.toISOString().slice(11, 16)} UTC`;
});
const grafana = computed(() => (role() === 'operator' ? webLink(session.value?.grafana_url) : undefined));
</script>

<template>
  <section class="card">
    <h2>Usage</h2>
    <p v-if="state === 'error'" class="fail">Couldn't load the usage. Reload the page to try again.</p>
    <template v-else-if="data">
      <ul>
        <li v-for="c in clients" :key="c">
          <span class="client">{{ c }}</span> <strong>{{ data.uses[c] }}</strong>
        </li>
      </ul>
      <p class="since">since {{ since }}, this replica</p>
    </template>
    <div v-else-if="skeleton" aria-busy="true">
      <div class="bar short"></div>
      <div class="bar"></div>
    </div>
    <a v-if="grafana" class="graf" :href="grafana" target="_blank" rel="noopener noreferrer">Usage over time in Grafana ↗</a>
  </section>
</template>

<style scoped>
h2 {
  margin: 0 0 var(--space-3);
  font-size: var(--text-md);
}
ul {
  margin: 0 0 var(--space-2);
  padding: 0;
  list-style: none;
}
li {
  display: flex;
  gap: var(--space-3);
  font-variant-numeric: tabular-nums;
}
.client {
  min-width: 4rem;
  color: var(--fg-secondary);
}
.since,
.fail {
  margin: 0;
  color: var(--fg-secondary);
  font-size: var(--text-md);
}
.graf {
  display: inline-flex;
  align-items: center;
  min-height: var(--target-min);
  margin-top: var(--space-2);
}
.bar {
  height: 1rem;
  margin-bottom: var(--space-3);
  border-radius: var(--radius-sm);
  background: var(--surface-3);
}
.bar.short {
  width: 40%;
}
</style>
