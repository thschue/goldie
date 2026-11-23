<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import AppShell from './components/AppShell.vue';
import SessionBanner from './auth/SessionBanner.vue';
import { role, signOut } from './auth/session';

const route = useRoute();
const router = useRouter();

// False until the first route (and so the first session check) has settled.
const ready = ref(false);
onMounted(() => void router.isReady().then(() => (ready.value = true)));

// A failed sign out keeps the person signed in and says so, with a retry.
const signOutFailed = ref(false);
// Keys the alert so each failure remounts it and a screen reader announces it again.
const failures = ref(0);
const retry = ref<HTMLButtonElement | null>(null);

async function onSignOut() {
  signOutFailed.value = !(await signOut());
  if (signOutFailed.value) {
    failures.value++;
    // The menu that held focus has closed; put it where the person can act.
    await nextTick();
    retry.value?.focus();
    return;
  }
  await router.push('/signin');
}
</script>

<template>
  <main v-if="!ready" class="boot-loading"><div role="status"><h1>Loading…</h1></div></main>
  <!-- The role is the session's; the public route (sign in) has no session, so no role chip. -->
  <AppShell v-else :role="role()" :account="!route.meta.public" @signout="onSignOut">
    <SessionBanner />
    <div v-if="signOutFailed" :key="failures" role="alert" class="signout-error">
      Could not sign you out. You are still signed in.
      <button ref="retry" type="button" @click="onSignOut">Retry</button>
    </div>
    <!-- Keyed by path: a route change replaces the view, so the focused h1 of
         the old one is gone and back or forward leaves focus at the page start.
         This also resets per-view state on every path change (intended while
         every route is a stateless view). A view whose state must survive a
         path change needs another key; see docs/design/ui-handoff.md A8. -->
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
  border: 1px solid var(--danger-border);
  border-radius: var(--radius-md);
  background: var(--danger-bg);
  color: var(--danger-ink);
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
