<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue';

// CommandSnippet, inline (handoff §4): a command on the code surface with a
// copy button. The button's name is fixed ("Copy command"); the result is
// announced through a polite status, never by renaming the button.
const props = defineProps<{ command: string }>();

const status = ref('');
let timer: ReturnType<typeof setTimeout> | undefined;

async function copy() {
  try {
    await navigator.clipboard.writeText(props.command);
    status.value = 'Copied';
  } catch {
    status.value = 'Copy failed. Select the command and copy it.';
  }
  clearTimeout(timer);
  timer = setTimeout(() => (status.value = ''), 2500);
}

onBeforeUnmount(() => clearTimeout(timer));
</script>

<template>
  <div class="snippet">
    <code>{{ command }}</code>
    <button type="button" aria-label="Copy command" @click="copy">Copy</button>
    <span role="status" class="status">{{ status }}</span>
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
