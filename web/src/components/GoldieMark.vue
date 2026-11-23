<template>
  <!-- goldie's original mark (§10): a winding path to a gold destination, no Phippy artwork. -->
  <svg class="mark" viewBox="0 0 32 32" width="32" height="32" aria-hidden="true" focusable="false">
    <rect class="tile" width="32" height="32" rx="8" />
    <path class="path" d="M9 25c0-5 5-5.5 7.5-8S15 10 22 9" />
    <circle class="dot" cx="23" cy="9" r="3.5" />
  </svg>
</template>

<style scoped>
.tile {
  fill: var(--color-brand-primary);
}
.path {
  fill: none;
  stroke: var(--color-on-brand);
  stroke-width: 3;
  stroke-linecap: round;
}
.dot {
  fill: var(--color-brand-accent);
}
</style>
