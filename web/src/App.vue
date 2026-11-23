<script setup lang="ts">
import { ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import AppShell from './components/AppShell.vue';
import SessionBanner from './auth/SessionBanner.vue';
import { role, signOut } from './auth/session';

const route = useRoute();
const router = useRouter();

// A failed sign out keeps the person signed in and says so, with a retry.
const signOutFailed = ref(false);

async function onSignOut() {
  signOutFailed.value = !(await signOut());
  if (!signOutFailed.value) await router.push('/signin');
}
</script>

<template>
  <!-- The role is the session's; the public route (sign in) has no session, so no role chip. -->
  <AppShell :role="role()" :account="!route.meta.public" @signout="onSignOut">
    <SessionBanner />
    <div v-if="signOutFailed" role="alert" class="signout-error">
      Could not sign you out. You are still signed in.
      <button type="button" @click="onSignOut">Retry</button>
    </div>
    <!-- Keyed by path: a route change replaces the view, so the focused h1 of
         the old one is gone and back or forward leaves focus at the page start. -->
    <RouterView v-slot="{ Component, route: view }">
      <component :is="Component" :key="view.path" />
    </RouterView>
  </AppShell>
</template>

<style scoped>
.signout-error {
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
button {
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  border: 1px solid currentColor;
  border-radius: var(--radius-md);
  background: transparent;
  color: inherit;
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}
</style>
