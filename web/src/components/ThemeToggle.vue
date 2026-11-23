<script setup lang="ts">
import { ref } from 'vue';

// ThemeToggle (handoff §4): cycles system → light → dark. The choice is kept
// in localStorage under the key public/theme-init.js reads before first paint;
// "system" removes data-theme, so goldie.css follows prefers-color-scheme.
type Theme = 'system' | 'light' | 'dark';
const storageKey = 'goldie-theme';
const order: Theme[] = ['system', 'light', 'dark'];

function stored(): Theme {
  try {
    const value = window.localStorage.getItem(storageKey);
    return value === 'light' || value === 'dark' ? value : 'system';
  } catch {
    return 'system';
  }
}

const theme = ref<Theme>(stored());

function next() {
  theme.value = order[(order.indexOf(theme.value) + 1) % order.length];
  if (theme.value === 'system') {
    document.documentElement.removeAttribute('data-theme');
  } else {
    document.documentElement.setAttribute('data-theme', theme.value);
  }
  try {
    window.localStorage.setItem(storageKey, theme.value);
  } catch {
    // Storage is off: the choice lasts until the page is left.
  }
}
</script>

<template>
  <button type="button" class="icon-button" :aria-label="`Theme: ${theme}`" :title="`Theme: ${theme}`" @click="next">
    <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true" focusable="false">
      <template v-if="theme === 'light'">
        <circle cx="12" cy="12" r="4" />
        <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
      </template>
      <path v-else-if="theme === 'dark'" d="M20 14.5A8 8 0 0 1 9.5 4 8 8 0 1 0 20 14.5z" />
      <template v-else>
        <rect x="3" y="4" width="18" height="12" rx="2" />
        <path d="M8 20h8M12 16v4" />
      </template>
    </svg>
  </button>
</template>

