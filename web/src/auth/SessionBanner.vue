<script setup lang="ts">
import { useRoute } from 'vue-router';
import { ended } from './session';

// The session-ended banner (handoff §5.1): non-blocking, the view stays.
// "Sign in again" returns to where the person was.
const route = useRoute();
</script>

<template>
  <div v-if="ended && !route.meta.public" role="status" class="banner">
    Your session ended.
    <RouterLink :to="{ path: '/signin', query: { next: route.fullPath } }">Sign in again</RouterLink>
    to keep going.
  </div>
</template>

<style scoped>
.banner {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  max-width: 760px;
  margin: 0 0 var(--space-6);
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--miss-border);
  border-radius: var(--radius-md);
  background: var(--miss-bg);
  color: var(--miss-ink);
}
a {
  display: inline-flex;
  align-items: center;
  min-height: var(--target-min);
  color: inherit;
  font-weight: 600;
}
</style>
