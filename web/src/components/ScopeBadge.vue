<script setup lang="ts">
import { computed } from 'vue';

// ScopeBadge (handoff §6): the Scope an Item was served from, outline plus an icon so the
// meaning never rides on colour. `scope` is global, host/org/repo or local; shown as text.
const props = defineProps<{ scope: string }>();
const kind = computed(() => (props.scope === 'global' ? 'global' : props.scope === 'local' ? 'local' : 'project'));
</script>

<template>
  <span class="scope">
    <svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true" focusable="false">
      <path v-if="kind === 'global'" d="M6 1.5a4.5 4.5 0 100 9 4.5 4.5 0 000-9zM1.5 6h9M6 1.5c-2 2.2-2 6.8 0 9M6 1.5c2 2.2 2 6.8 0 9" />
      <path v-else-if="kind === 'local'" d="M2 3h8v5H2zM4 10.5h4M6 8v2.5" />
      <path v-else d="M2 2.5h8v7H2zM2 5h8" />
    </svg>
    <bdi>{{ scope }}</bdi>
  </span>
</template>

<style scoped>
.scope {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  max-width: 100%;
  padding: 2px var(--space-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  color: var(--fg-secondary);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}
svg {
  flex: none;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.5;
  stroke-linecap: round;
}
</style>
