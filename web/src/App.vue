<script setup lang="ts">
import AppShell from './components/AppShell.vue';
</script>

<template>
  <!-- Every visitor is a reader until sign-in (#25) brings the role. -->
  <AppShell>
    <RouterView />
  </AppShell>
</template>
