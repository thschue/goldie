<script setup lang="ts">
import { onMounted, ref } from 'vue';

// The full-page "API unreachable" state (handoff §5.1). It names no address and no setting:
// the envelope behind it is never shown.
defineEmits<{ retry: [] }>();

const retry = ref<HTMLButtonElement | null>(null);
// Retry takes focus when the state appears, and again after a failed retry (the state is
// mounted again), like the session check notice.
onMounted(() => retry.value?.focus());
</script>

<template>
  <!-- The text nodes keep a space between the parts: the region reads as one sentence. -->
  <div role="alert" class="unreachable">
    <h1>API unreachable.</h1>
    {{ ' ' }}
    <p>goldie’s API isn’t answering right now. We’ll keep trying.</p>
    {{ ' ' }}
    <button ref="retry" type="button" @click="$emit('retry')">Retry now</button>
  </div>
</template>

<style scoped>
.unreachable {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-4);
  max-width: 760px;
  margin: 0 0 var(--space-6);
  padding: var(--space-4);
  border: 1px solid var(--danger-border);
  border-radius: var(--radius-md);
  background: var(--danger-bg);
  color: var(--danger-ink);
}
h1 {
  flex-basis: 100%;
  margin: 0;
}
p {
  margin: 0;
}
button {
  min-height: var(--target-min);
  min-width: var(--target-min);
  padding: 0 var(--space-4);
  border: 1px solid currentColor;
  border-radius: var(--radius-md);
  background: transparent;
  color: inherit;
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}
</style>
