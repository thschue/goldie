<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useLoad } from '../api/useLoad';
import MarkdownBody from '../articles/MarkdownBody.vue';
import { projectOf } from '../projects/api';
import { getInstructions, instructionTargets, plain, targetOf, type InstructionTarget } from './api';
import AssetList from './AssetList.vue';
import CommandSnippet from './CommandSnippet.vue';

// The Instructions tab (handoff §3.5, #590): the server's managed block for one target, its
// digest and the command that writes it. The target is the URL's ?target=, pushed on a click so
// Back and a reload keep it. The block is untrusted Markdown and goes through MarkdownBody.
const route = useRoute();
const router = useRouter();
const project = computed(() => projectOf(route.query));
const target = computed(() => targetOf(route.query.target));
// One key per (target, Project): a change aborts the older load, so a stale answer never lands.
const key = computed(() => (project.value ? `${target.value}|${project.value}` : ''));
const { state, data, failure, wait, skeleton, limited, retry } = useLoad(key, (k, signal) => {
  const i = k.indexOf('|');
  return getInstructions(targetOf(k.slice(0, i)), k.slice(i + 1), signal);
});
const items = computed(() => (data.value ? [{ name: target.value }] : []));

const choose = (t: InstructionTarget) => {
  if (t !== target.value) void router.push({ path: route.path, query: { ...route.query, target: t } });
};

const copied = ref('');
let timer: ReturnType<typeof setTimeout> | undefined;
async function copyDigest() {
  try {
    await navigator.clipboard.writeText(data.value?.digest ?? '');
    copied.value = 'Copied';
  } catch {
    copied.value = 'Copy failed. Select the digest and copy it.';
  }
  clearTimeout(timer);
  timer = setTimeout(() => (copied.value = ''), 2500);
}
onBeforeUnmount(() => clearTimeout(timer));
</script>

<template>
  <div class="switch" role="group" aria-label="Target file">
    <button
      v-for="t in instructionTargets"
      :key="t.target"
      type="button"
      :aria-pressed="target === t.target"
      @click="choose(t.target)"
    >
      {{ t.file }}
    </button>
  </div>
  <AssetList
    noun="instructions"
    layout="list"
    :project="project"
    :state="state"
    :failure="failure"
    :wait="wait"
    :skeleton="skeleton"
    :limited="limited"
    :retry="retry"
    :hint="data?.hint"
    :items="items"
  >
    <template #default>
      <article class="block">
        <div class="digest">
          <span class="label">Digest</span>
          <code>{{ plain(data?.digest) }}</code>
          <button type="button" class="copy" aria-label="Copy digest" @click="copyDigest">Copy</button>
          <span role="status" class="sr-only">{{ copied }}</span>
        </div>
        <MarkdownBody class="body" contiguous :source="plain(data?.block)" />
        <CommandSnippet :command="`goldie instructions apply --target ${target}`" />
      </article>
    </template>
  </AssetList>
</template>

<style scoped>
.switch {
  display: inline-flex;
  max-width: 100%;
  margin-bottom: var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  overflow: hidden;
}
.switch button {
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  border: 0;
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
  font-family: var(--font-mono);
  cursor: pointer;
}
.switch button[aria-pressed='true'] {
  background: var(--brand-tint-bg, var(--surface-3));
  color: var(--brand-tint-ink);
  font-weight: 700;
  box-shadow: inset 0 -3px 0 currentColor;
}
.block {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-3);
  min-width: 0;
  max-width: 100%;
}
.block > .body {
  width: 100%;
  min-width: 0;
}
.digest {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  max-width: 100%;
}
.label {
  color: var(--fg-secondary);
}
code {
  min-width: 0;
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
  word-break: break-all;
}
.copy {
  min-width: var(--target-min);
  min-height: var(--target-min);
  padding: 0 var(--space-3);
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--brand-tint-ink);
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}
.copy:hover {
  background: var(--surface-3);
}
</style>
