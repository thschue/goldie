<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue';

// The Home search field (handoff §3.2): the label, the input, the clear button and the "/" key.
// Home and the Project's Home share it; each owns what a submit and a clear do. The submit is
// handled in JavaScript: the UI runs under form-action 'none'.
const draft = defineModel<string>({ required: true });
const emit = defineEmits<{ submit: []; clear: [] }>();
const input = ref<HTMLInputElement>();
defineExpose({ focus: () => input.value?.focus() });

// "/" focuses the field from anywhere but a place that takes typing.
function onKeydown(e: KeyboardEvent) {
  if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return;
  const t = e.target;
  if (t instanceof HTMLElement && (t.isContentEditable || /^(input|textarea|select)$/i.test(t.tagName))) return;
  e.preventDefault();
  input.value?.focus();
}
onMounted(() => document.addEventListener('keydown', onKeydown));
onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown));
</script>

<template>
  <form class="search" role="search" @submit.prevent="emit('submit')">
    <label for="home-search" class="label">Search the golden path</label>
    <div class="field">
      <input
        id="home-search"
        ref="input"
        v-model="draft"
        type="search"
        class="input"
        placeholder="database, logging, retries, feature flags…"
        autocomplete="off"
        aria-keyshortcuts="/"
      />
      <button v-if="draft" type="button" class="icon-button clear" aria-label="Clear search" @click="emit('clear')">
        <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true" focusable="false"><path d="M6 6l12 12M18 6L6 18" /></svg>
      </button>
      <kbd v-else class="hint" aria-hidden="true">/</kbd>
    </div>
  </form>
</template>

<style scoped>
.search {
  max-width: 940px;
  margin-bottom: var(--space-6);
}
.label {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
.field {
  position: relative;
}
.input {
  width: 100%;
  height: 58px;
  padding: 0 calc(var(--target-min) + var(--space-3)) 0 var(--space-4);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
}
.input::placeholder {
  color: var(--fg-muted);
}
.clear {
  position: absolute;
  top: 7px;
  right: var(--space-2);
  border: 0;
}
.hint {
  position: absolute;
  top: 50%;
  right: var(--space-4);
  transform: translateY(-50%);
  padding: 0 var(--space-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}

/* §7: the field is 52 px below 768 px; the clear button stays centred. */
@media (max-width: 767px) {
  .input {
    height: 52px;
  }
  .clear {
    top: calc((52px - var(--target-min)) / 2);
  }
}
</style>
