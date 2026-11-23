<script setup lang="ts">
import { computed, nextTick } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { assetTabs, type AssetTab } from './tabs';

// AssetTabs (handoff §3.5, §9 finding A3): WAI-ARIA tabs with automatic
// activation. The URL names the selected tab, so a reload and browser back keep
// it, and the ?project= query rides along on every switch. Only the selected
// tab is in the tab order (roving tabindex); Left and Right wrap, Home and End
// jump to the ends. Each tab panel is a slot, empty until the later slices.
const props = defineProps<{ selected: AssetTab }>();

const route = useRoute();
const router = useRouter();
const index = computed(() => assetTabs.findIndex((t) => t.slug === props.selected));

// App.vue keys the view by route.path, so a tab change remounts this component
// and the old button is gone: focus the new tab by id once the route has settled.
async function go(i: number, replace: boolean) {
  const slug = assetTabs[(i + assetTabs.length) % assetTabs.length]!.slug;
  const to = { path: `/assets/${slug}`, query: route.query };
  await (replace ? router.replace(to) : router.push(to));
  await nextTick();
  document.getElementById(`asset-tab-${slug}`)?.focus();
}

function onKeydown(e: KeyboardEvent) {
  if (e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
  const target = { ArrowRight: index.value + 1, ArrowLeft: index.value - 1, Home: 0, End: assetTabs.length - 1 }[e.key];
  if (target === undefined) return;
  e.preventDefault();
  void go(target, true);
}
</script>

<template>
  <div role="tablist" aria-label="Agent assets" class="tabs" @keydown="onKeydown">
    <button
      v-for="(t, i) in assetTabs"
      :id="`asset-tab-${t.slug}`"
      :key="t.slug"
      type="button"
      role="tab"
      :aria-selected="t.slug === selected"
      :aria-controls="`asset-panel-${t.slug}`"
      :tabindex="t.slug === selected ? 0 : -1"
      @click="void go(i, false)"
    >
      {{ t.name }}
    </button>
  </div>
  <div
    v-for="t in assetTabs"
    :id="`asset-panel-${t.slug}`"
    :key="t.slug"
    role="tabpanel"
    :aria-labelledby="`asset-tab-${t.slug}`"
    :hidden="t.slug !== selected"
  >
    <slot :name="t.slug" />
  </div>
</template>

<style scoped>
.tabs {
  display: flex;
  margin: var(--space-6) 0 var(--space-4);
  border-bottom: 1px solid var(--border-default);
}
[role='tab'] {
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  border: 0;
  border-bottom: 3px solid transparent;
  background: transparent;
  color: var(--fg-secondary);
  font: inherit;
  font-weight: 500;
  cursor: pointer;
}
[role='tab']:hover {
  background: var(--surface-3);
}
/* The selected tab: weight and a bar as well as colour (§1.5). */
[role='tab'][aria-selected='true'] {
  border-bottom-color: var(--brand);
  color: var(--brand-tint-ink);
  font-weight: 700;
}
</style>
