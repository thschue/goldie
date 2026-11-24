<script setup lang="ts">
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { useAssetLoad } from './useAssetLoad';
import ScopeBadge from '../components/ScopeBadge.vue';
import { projectOf } from '../projects/api';
import { agentPath, listAgents, plain } from './api';
import AssetList from './AssetList.vue';

// The Agents tab (handoff §3.5, §5.1, §5.2): a row per agent for the Project in the URL,
// sorted by name, with the path `goldie pull` writes it to. No usage figures in the MVP cut.
// Descriptions are Source text and render as text only.
const route = useRoute();
const project = computed(() => projectOf(route.query));
const { state, data, failure, wait, skeleton, limited, retry } = useAssetLoad(project, listAgents);
const agents = computed(() => data.value?.agents ?? []);
</script>

<template>
  <AssetList
    noun="agents"
    :project="project"
    :state="state"
    :failure="failure"
    :wait="wait"
    :skeleton="skeleton"
    :limited="limited"
    :retry="retry"
    :hint="data?.hint"
    :items="agents"
  >
    <template #default="{ item: s }">
      <div class="head">
        <h2>{{ plain(s.name) }}</h2>
        <ScopeBadge v-if="s.scope" :scope="plain(s.scope)" />
      </div>
      <p v-if="s.description" class="desc">{{ plain(s.description) }}</p>
      <code class="path">{{ agentPath(s.name) }}</code>
    </template>
  </AssetList>
</template>

<style scoped>
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
  max-width: 100%;
}
h2 {
  margin: 0;
  font-family: var(--font-mono);
  font-size: var(--text-lg);
  overflow-wrap: anywhere;
}
.desc {
  margin: 0;
  max-width: 100%;
  overflow-wrap: anywhere;
  color: var(--fg-default);
}
code {
  font-family: var(--font-mono);
}
.path {
  max-width: 100%;
  overflow-wrap: anywhere;
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
</style>
