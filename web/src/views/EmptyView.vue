<script setup lang="ts">
import { useRoute } from 'vue-router';

const route = useRoute();
</script>

<template>
  <h1>{{ route.meta.title }}</h1>
  <!-- Copy: handoff §8, "Empty state". Neutral, never gold (§1.3). -->
  <div role="status" class="panel">Nothing to show yet. Content appears here as goldie's pages come online.</div>
</template>

<style scoped>
.panel {
  max-width: 760px;
  padding: var(--space-6);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-2);
  color: var(--fg-secondary);
}
</style>
