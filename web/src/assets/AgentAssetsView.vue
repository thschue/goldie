<script setup lang="ts">
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import AssetTabs from './AssetTabs.vue';
import CommandSnippet from './CommandSnippet.vue';
import LoopsTab from './LoopsTab.vue';
import SkillsTab from './SkillsTab.vue';
import type { AssetTab } from './tabs';

// The Agent assets frame (handoff §3.5): the h1, the pull snippet and the tabs.
// The route only matches the known tabs; the Skills and Loops tabs have content (#587, #647).
const route = useRoute();
const selected = computed(() => route.params.tab as AssetTab);
</script>

<template>
  <h1>Agent assets</h1>
  <CommandSnippet command="goldie pull --dry-run" />
  <AssetTabs :selected="selected">
    <template #skills><SkillsTab v-if="selected === 'skills'" /></template>
    <template #loops><LoopsTab v-if="selected === 'loops'" /></template>
  </AssetTabs>
</template>
