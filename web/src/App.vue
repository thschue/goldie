<script setup lang="ts">
import AppShell from './components/AppShell.vue';
</script>

<template>
  <!-- Every visitor is a reader until sign-in (#25) brings the role. -->
  <AppShell>
    <!-- Keyed by path: a route change replaces the view, so the focused h1 of
         the old one is gone and back or forward leaves focus at the page start. -->
    <RouterView v-slot="{ Component, route }">
      <component :is="Component" :key="route.path" />
    </RouterView>
  </AppShell>
</template>
