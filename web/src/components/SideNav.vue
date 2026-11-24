<script setup lang="ts">
// The side nav (handoff §2.1): every entry links to its §2.2 route, and the
// router marks the current one with aria-current="page". Operator-only entries
// are absent for readers, who get the Reader nav note (§8) instead.
import { computed, watch } from 'vue';
import { useRoute } from 'vue-router';
import { projectQuery } from '../projects/api';
import { assetTabs } from '../assets/tabs';
import { failing, loadFailing } from '../sources/api';

const props = defineProps<{ role: 'reader' | 'operator' }>();

// The failing-Source count (#633) is an operator's: a reader never asks for it.
watch(() => props.role, (r) => r === 'operator' && void loadFailing(), { immediate: true });

const assets = assetTabs.map((t) => [t.name, `/assets/${t.slug}`]);

// Every link keeps the selected Project (#729); with none selected the query is empty.
const route = useRoute();
const query = computed(() => projectQuery(route.query));
const to = (path: string) => ({ path, query: query.value });
// Articles stays current on an article page too (#812): the page belongs to the index.
const onArticles = computed(() => route.path === '/articles' || route.path.startsWith('/items/article/'));
</script>

<template>
  <nav aria-label="Main" class="nav">
    <slot />
    <ul>
      <li><RouterLink :to="to('/')">Home</RouterLink></li>
      <li><RouterLink v-slot="{ href, navigate }" :to="to('/articles')" custom>
          <a :href="href" :aria-current="onArticles ? 'page' : undefined" @click="navigate">Articles</a>
        </RouterLink></li>
      <li><RouterLink :to="to('/decisions')">Decisions</RouterLink></li>
    </ul>
    <p id="nav-assets" class="group">Agent assets</p>
    <ul aria-labelledby="nav-assets">
      <li v-for="[name, path] in assets" :key="path"><RouterLink :to="to(path)">{{ name }}</RouterLink></li>
    </ul>
    <template v-if="role === 'operator'">
      <p id="nav-operate" class="group">Operate</p>
      <ul aria-labelledby="nav-operate">
        <li><RouterLink :to="to('/ops/activity')">Activity</RouterLink></li>
        <li>
          <RouterLink :to="to('/ops/sources')">Sources<strong v-if="failing" class="count">{{ ` ${failing} failing` }}</strong></RouterLink>
        </li>
      </ul>
    </template>
    <p v-else class="note">Reader view: Activity and Sources are for operators and aren't shown.</p>
  </nav>
</template>

<style scoped>
.nav {
  padding: var(--space-4) var(--space-3);
}
ul {
  margin: 0;
  padding: 0;
  list-style: none;
}
a {
  display: flex;
  align-items: center;
  min-height: var(--target-min);
  padding: 0 var(--space-3);
  border-radius: var(--radius-sm);
  color: var(--fg-default);
  text-decoration: none;
}
a {
  gap: var(--space-2);
}
.count {
  color: var(--danger-ink);
  font-size: var(--text-sm);
}
a:hover {
  background: var(--surface-3);
}
/* The current page: weight and an edge bar as well as colour (§1.5). */
a[aria-current='page'] {
  background: var(--brand-tint);
  box-shadow: inset 3px 0 0 var(--brand);
  color: var(--brand-tint-ink);
  font-weight: 600;
}
.group {
  margin: var(--space-6) var(--space-3) var(--space-2);
  color: var(--fg-muted);
  font-size: var(--text-sm);
  font-weight: 600;
}
.note {
  margin: var(--space-6) var(--space-3) 0;
  color: var(--fg-muted);
  font-size: var(--text-sm);
}
</style>
