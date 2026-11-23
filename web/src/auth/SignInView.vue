<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { safeNext } from './next';
import { signIn } from './session';

// Sign in (handoff §2.3, §5.1, §8): the access-token form. It submits in
// JavaScript, never as a native post (the UI runs under form-action 'none'),
// clears the field once sent, and never puts the token anywhere but the one
// POST. GitHub sign-in is #25.
const route = useRoute();
const router = useRouter();

// goldie has no org setting yet, so the lead names the organisation generically.
const org = 'your organisation';

const token = ref('');
const field = ref<HTMLInputElement>();
const busy = ref(false);
const error = ref<{ message: string; hint: string } | null>(null);
const wait = ref(0);
let timer: ReturnType<typeof setInterval> | undefined;

const limited = computed(() => wait.value > 0);
const describedby = computed(() => ['token-help', error.value ? 'signin-error' : ''].filter(Boolean).join(' '));

function countdown(seconds: number) {
  const until = Date.now() + seconds * 1000;
  wait.value = seconds;
  clearInterval(timer);
  timer = setInterval(() => {
    wait.value = Math.max(0, Math.ceil((until - Date.now()) / 1000));
    if (wait.value === 0) clearInterval(timer);
  }, 250);
}

async function submit() {
  if (busy.value || limited.value || token.value === '') return;
  const sent = token.value;
  token.value = '';
  busy.value = true;
  error.value = null;
  const result = await signIn(sent);
  busy.value = false;
  if (result.ok) {
    await router.replace(safeNext(route.query.next));
    return;
  }
  if (result.kind === 'limited') {
    countdown(result.seconds);
  } else {
    error.value = {
      message:
        result.kind === 'refused'
          ? "That token wasn't accepted. Check it and try again, or ask your platform team for a new one."
          : "Couldn't sign in. Try again in a moment.",
      hint: result.hint,
    };
  }
  await nextTick();
  field.value?.focus();
}

onBeforeUnmount(() => clearInterval(timer));
</script>

<template>
  <div class="card">
    <h1>Sign in</h1>
      <p class="lead">The golden path for <strong><code>{{ org }}</code></strong> — the same guidance your coding agents get.</p>
      <form novalidate @submit.prevent="submit">
        <label for="token">Access token</label>
        <input
          id="token"
          ref="field"
          v-model="token"
          type="password"
          name="token"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          required
          :aria-describedby="describedby"
          :aria-invalid="error ? 'true' : undefined"
        />
        <p id="token-help" class="help">Exchanged once for a secure session. The token is never stored in your browser.</p>
        <div v-if="error" id="signin-error" role="alert" class="alert">
          {{ error.message }} <span v-if="error.hint">{{ error.hint }}</span>
        </div>
        <div v-if="limited" role="status" class="limited">Too many requests — try again in {{ wait }} s.</div>
        <button type="submit" class="submit" :disabled="busy || limited">Sign in</button>
      </form>
  </div>
</template>

<style scoped>
.card {
  width: 100%;
  margin: 0 auto;
  max-width: 440px;
  padding: var(--space-6);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-lg);
  background: var(--surface-2);
}
.lead {
  margin: 0 0 var(--space-6);
  color: var(--fg-secondary);
}
label {
  display: block;
  margin-bottom: var(--space-2);
  font-weight: 600;
}
input {
  display: block;
  width: 100%;
  min-height: var(--target-min);
  padding: 0 var(--space-3);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
}
.help {
  margin: var(--space-2) 0 var(--space-4);
  color: var(--fg-muted);
  font-size: var(--text-md);
}
.alert,
.limited {
  margin-bottom: var(--space-4);
  padding: var(--space-3) var(--space-4);
  border: 1px solid;
  border-radius: var(--radius-md);
  font-size: var(--text-md);
}
.alert {
  border-color: var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-ink);
}
.limited {
  border-color: var(--miss-border);
  background: var(--miss-bg);
  color: var(--miss-ink);
}
.submit {
  min-height: var(--target-min);
  padding: 0 var(--space-6);
  border: 1px solid var(--brand);
  border-radius: var(--radius-md);
  background: var(--brand);
  color: var(--surface-1);
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}
.submit:hover:not(:disabled) {
  background: var(--brand-hover);
}
.submit:disabled {
  border-color: var(--border-strong);
  background: var(--surface-3);
  color: var(--fg-muted);
  cursor: not-allowed;
}
</style>
