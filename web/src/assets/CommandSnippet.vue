<script setup lang="ts">
import { useCopy } from './useCopy';

// CommandSnippet, inline (handoff §4): a command on the code surface with a
// copy button. The button's name is fixed ("Copy command"); the result is
// announced through a polite live region, never by renaming the button. It is not role=status,
// so an empty one adds no status to the page; aria-atomic restores that role's whole-region read.
const props = defineProps<{ command: string }>();

const { status, copy: write } = useCopy('command');
const copy = () => write(props.command);
</script>

<template>
  <div class="snippet">
    <code>{{ command }}</code>
    {{ ' ' }}
    <button type="button" aria-label="Copy command" @click="copy">Copy</button>
    <span aria-live="polite" aria-atomic="true" class="status">{{ status }}</span>
  </div>
</template>

<style scoped>
.snippet {
  position: relative;
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-1) var(--space-1) var(--space-3);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-2);
}
code {
  min-width: 0;
  overflow-wrap: anywhere;
  color: var(--fg-default);
  font-size: var(--text-md);
}
button {
  min-width: var(--target-min);
  min-height: var(--target-min);
  padding: 0 var(--space-3);
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--brand-tint-ink);
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}
button:hover {
  background: var(--surface-3);
}
.status {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
</style>
