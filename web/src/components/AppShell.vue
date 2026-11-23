<script setup lang="ts">
import { ref } from 'vue';
import GoldieMark from './GoldieMark.vue';
import ThemeToggle from './ThemeToggle.vue';

// AppShell (handoff §3.1): the skip link, the top bar and main. The side nav
// and the drawer behind the menu button come with the next slice of #166.
const main = ref<HTMLElement>();

function skipToMain() {
  main.value?.focus();
}
</script>

<template>
  <div class="shell">
    <header class="topbar">
      <a class="skip-link" href="#main" @click.prevent="skipToMain">Skip to content</a>
      <a href="/" class="brand"><GoldieMark /><span>goldie</span></a>
      <div class="actions">
        <ThemeToggle />
      </div>
    </header>
    <main id="main" ref="main" tabindex="-1">
      <slot />
    </main>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-rows: var(--topbar-height) 1fr;
  min-height: 100vh;
}
.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  height: var(--topbar-height);
  padding: 0 var(--space-6);
  border-bottom: 1px solid var(--border-default);
  background: var(--surface-1);
}
.skip-link {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
.skip-link:focus {
  position: fixed;
  top: var(--space-2);
  left: var(--space-2);
  z-index: 30;
  display: inline-flex;
  align-items: center;
  width: auto;
  height: auto;
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  clip-path: none;
  border-radius: var(--radius-sm);
  background: var(--surface-1);
  box-shadow: var(--shadow-lg);
  font-weight: 600;
}
.brand {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  min-height: var(--target-min);
  color: var(--fg-default);
  font-family: var(--font-display);
  font-size: var(--text-display-sm);
  font-weight: 600;
  text-decoration: none;
}
.actions {
  display: flex;
  gap: var(--space-2);
}
main {
  min-width: 0;
  padding: var(--space-10);
}
main:focus-visible {
  outline: none;
}

@media (width < 1024px) {
  .topbar {
    padding: 0 var(--space-4);
  }
  main {
    padding: var(--space-6) var(--space-4);
  }
}
</style>
