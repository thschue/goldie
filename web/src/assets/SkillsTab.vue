<script setup lang="ts">
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { useAssetLoad } from './useAssetLoad';
import ScopeBadge from '../components/ScopeBadge.vue';
import { projectOf } from '../projects/api';
import { listSkills, plain, promptName } from './api';
import AssetList from './AssetList.vue';

// The Skills tab (handoff §3.5, §5.1, §5.2): a card per skill for the Project in the URL,
// sorted by name. Descriptions are Source text and render as text only.
const route = useRoute();
const project = computed(() => projectOf(route.query));
const { state, data, failure, wait, skeleton, limited, retry } = useAssetLoad(project, listSkills);
const skills = computed(() => data.value?.skills ?? []);
</script>

<template>
  <AssetList
    noun="skills"
    :project="project"
    :state="state"
    :failure="failure"
    :wait="wait"
    :skeleton="skeleton"
    :limited="limited"
    :retry="retry"
    :hint="data?.hint"
    :items="skills"
  >
    <template #default="{ item: s }">
      <div class="head">
        <h2>{{ s.name }}</h2>
        <ScopeBadge v-if="s.scope" :scope="plain(s.scope)" />
      </div>
      <p v-if="s.description" class="desc">{{ plain(s.description) }}</p>
      <code class="prompt">{{ promptName(s.name) }}</code>
      <template v-if="s.requires_sync">
        <span class="sync">requires_sync</span>
        <p class="pull">Contains scripts, so agents won't run it from text. Run <code>goldie pull</code> to install it.</p>
      </template>
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
.desc,
.pull {
  margin: 0;
  max-width: 100%;
  overflow-wrap: anywhere;
}
.desc {
  color: var(--fg-default);
}
.pull {
  color: var(--miss-ink);
  font-size: var(--text-sm);
}
code {
  font-family: var(--font-mono);
}
.prompt {
  max-width: 100%;
  overflow-wrap: anywhere;
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
.sync {
  padding: 2px var(--space-2);
  border: 1px solid var(--miss-border);
  border-radius: var(--radius-sm);
  background: var(--miss-bg);
  color: var(--miss-ink);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
</style>
