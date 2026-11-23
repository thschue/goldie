<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue';

// The role chip and its menu (handoff §4, §5.3): reader is an outline, operator
// a blue fill. The menu has one item, Sign out. Esc and a click elsewhere close
// it; Esc returns focus to the chip.
defineProps<{ role: 'reader' | 'operator' }>();
const emit = defineEmits<{ signout: [] }>();

const open = ref(false);
const root = ref<HTMLElement>();
const chip = ref<HTMLButtonElement>();
const item = ref<HTMLButtonElement>();

async function toggle() {
  open.value = !open.value;
  if (open.value) {
    await nextTick();
    item.value?.focus();
  }
}

function close(returnFocus: boolean) {
  if (!open.value) return;
  open.value = false;
  if (returnFocus) chip.value?.focus();
}

function onKeydown(e: KeyboardEvent) {
  if (open.value && e.key === 'Escape') {
    e.preventDefault();
    close(true);
  }
}

const onPointerdown = (e: Event) => {
  if (open.value && e.target instanceof Node && !root.value?.contains(e.target)) close(false);
};

// Tabbing out of the menu closes it without stealing focus back.
function onFocusout(e: FocusEvent) {
  if (open.value && e.relatedTarget instanceof Node && !root.value?.contains(e.relatedTarget)) close(false);
}

function signOut() {
  close(false);
  emit('signout');
}

onMounted(() => {
  document.addEventListener('keydown', onKeydown);
  document.addEventListener('pointerdown', onPointerdown);
});
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown);
  document.removeEventListener('pointerdown', onPointerdown);
});
</script>

<template>
  <div ref="root" class="role-menu" @focusout="onFocusout">
    <button
      ref="chip"
      type="button"
      class="chip"
      :class="role"
      aria-haspopup="menu"
      aria-controls="role-menu"
      :aria-expanded="open"
      @click="toggle"
    >
      {{ role }}
    </button>
    <div v-if="open" id="role-menu" class="menu" role="menu" aria-label="Account">
      <button ref="item" type="button" role="menuitem" @click="signOut">Sign out</button>
    </div>
  </div>
</template>

<style scoped>
.role-menu {
  position: relative;
}
.chip {
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--fg-default);
  font: inherit;
  font-size: var(--text-md);
  font-weight: 600;
  cursor: pointer;
}
.chip:hover {
  background: var(--surface-3);
}
/* surface-1 is the on-brand colour in both themes: white on blue, navy on light blue. */
.chip.operator {
  border-color: var(--brand);
  background: var(--brand);
  color: var(--surface-1);
}
.chip.operator:hover {
  background: var(--brand-hover);
}
.menu {
  position: absolute;
  top: 100%;
  right: 0;
  z-index: 5;
  min-width: 160px;
  margin-top: var(--space-2);
  padding: var(--space-2);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-1);
  box-shadow: var(--shadow-lg);
}
.menu button {
  display: flex;
  align-items: center;
  width: 100%;
  min-height: var(--target-min);
  padding: 0 var(--space-3);
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--fg-default);
  font: inherit;
  font-size: var(--text-md);
  text-align: left;
  cursor: pointer;
}
.menu button:hover {
  background: var(--surface-3);
}
</style>
