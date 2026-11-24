<script setup lang="ts">
import { nextTick, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import AppShell from './components/AppShell.vue';
import GoldieMark from './components/GoldieMark.vue';
import ApiUnreachable from './components/ApiUnreachable.vue';
import { attempt, unreachable } from './api/reachability';
import SessionBanner from './auth/SessionBanner.vue';
import { checkFailed, refresh, role, signOut } from './auth/session';

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

// The session check got no answer: the routed page waits behind a notice, and
// Retry asks again. A 401 on the retry is a real "signed out".
const checking = ref(false);
// Keys the alert so a repeat failure remounts it and is announced again.
const checkFailures = ref(0);
const checkRetry = ref<HTMLButtonElement | null>(null);
// Retry takes focus when the alert first appears and after each failure.
// checkFailures is a source only so that a repeat failure retriggers the focus; do not drop it.
watch([ready, checkFailed, checkFailures], async ([isReady, failed]) => {
  if (!isReady || !failed) return;
  await nextTick();
  checkRetry.value?.focus();
});
async function retryCheck() {
  if (checking.value) return;
  checking.value = true;
  const ok = await refresh();
  checking.value = false;
  if (!ok) {
    await router.replace({ path: '/signin', query: { next: route.fullPath } });
    return;
  }
  if (checkFailed.value) {
    // Still no answer: a new alert replaces the old one and takes focus.
    checkFailures.value++;
  }
}

// Retry now: a failed first session check asks again (an answer clears the state); otherwise
// the view is mounted again and repeats its calls, and a repeat failure brings the state back.
async function retryNow() {
  if (checkFailed.value) {
    await retryCheck();
    return;
  }
  unreachable.value = false;
  attempt.value++;
}

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
  <!-- The public route (sign in, handoff §2.3) is a bare page: logo, then the card. Before sign-in
       every nav link would redirect back, so it has no shell. -->
  <main v-else-if="route.meta.public" class="bare">
    <div class="logo"><GoldieMark /><span>goldie</span></div>
    <RouterView v-slot="{ Component, route: view }">
      <component :is="Component" :key="`${view.path}#${attempt}`" />
    </RouterView>
  </main>
  <!-- The role is the session's; the shell is for the signed-in routes, so there is always a role chip. -->
  <AppShell v-else :role="role()" :account="!checkFailed" @signout="onSignOut">
    <SessionBanner />
    <div v-if="signOutFailed" :key="failures" role="alert" class="page-alert">
      Could not sign you out. You are still signed in.
      <button ref="retry" type="button" @click="onSignOut">Retry</button>
    </div>
    <ApiUnreachable v-if="unreachable" @retry="retryNow" />
    <div v-else-if="checkFailed" :key="checkFailures" role="alert" class="page-alert">
      <h1>Could not check your session</h1>
      <p>Your sign-in is not affected.</p>
      <button ref="checkRetry" type="button" :aria-disabled="checking" @click="retryCheck">Retry</button>
    </div>
    <!-- Keyed by path: a route change replaces the view, so the focused h1 of
         the old one is gone and back or forward leaves focus at the page start.
         This also resets per-view state on every path change (intended while
         every route is a stateless view). A view whose state must survive a
         path change needs another key; see docs/design/ui-handoff.md A8. -->
    <RouterView v-else v-slot="{ Component, route: view }">
      <component :is="Component" :key="`${view.path}#${attempt}`" />
    </RouterView>
  </AppShell>
</template>

<style scoped>
.bare {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-6);
  min-height: 100vh;
  padding: var(--space-6) var(--space-4);
  background: var(--surface-2);
}
.logo {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  font-size: var(--text-display-lg);
  font-weight: 700;
}
.page-alert {
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
h1 {
  flex-basis: 100%;
  margin: 0;
}
p {
  margin: 0;
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
/* A check in flight: the label keeps full contrast (AA), the border turns
   dashed and the cursor shows progress. aria-disabled keeps focus on it. */
button[aria-disabled='true'] {
  border-style: dashed;
  cursor: progress;
}
</style>
