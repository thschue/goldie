<script setup lang="ts">
import { useRoute, useRouter } from 'vue-router';
import AppShell from './components/AppShell.vue';
import SessionBanner from './auth/SessionBanner.vue';
import { role, signOut } from './auth/session';

const route = useRoute();
const router = useRouter();

async function onSignOut() {
  await signOut();
  await router.push('/signin');
}
</script>

<template>
  <!-- The role is the session's; the public route (sign in) has no session, so no role chip. -->
  <AppShell :role="role()" :account="!route.meta.public" @signout="onSignOut">
    <SessionBanner />
    <!-- Keyed by path: a route change replaces the view, so the focused h1 of
         the old one is gone and back or forward leaves focus at the page start. -->
    <RouterView v-slot="{ Component, route: view }">
      <component :is="Component" :key="view.path" />
    </RouterView>
  </AppShell>
</template>
