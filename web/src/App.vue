<script setup lang="ts">
import { useRoute } from 'vue-router';
import AppShell from './components/AppShell.vue';

const route = useRoute();
</script>

<template>
  <!-- Every visitor is a reader until sign-in (#25) brings the role. -->
  <AppShell>
    <!-- Keyed by path: routes that share a component (EmptyView) get a fresh view,
         so the h1 a link focused is replaced on back or forward, and focus falls to
         the page start instead of staying on a recycled h1 (handoff A8). -->
    <RouterView v-slot="{ Component }">
      <component :is="Component" :key="route.path" />
    </RouterView>
  </AppShell>
</template>
