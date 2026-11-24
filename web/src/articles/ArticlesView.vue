<script setup lang="ts">
import { errorSentence } from '../api/errorSentence';
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { useAssetLoad } from '../assets/useAssetLoad';
import { plain } from '../assets/api';
import ScopeBadge from '../components/ScopeBadge.vue';
import { projectOf, projectQuery } from '../projects/api';
import { listArticles } from './api';

// The article index (handoff §3.2, #724): the articles of the Project in the URL as cards, each a
// link to its Item page. Everything from a Source is untrusted and renders as text (`plain`). With
// no Project the global view is requested.
const route = useRoute();
const project = computed(() => projectOf(route.query));
const { state, data, failure, wait, skeleton, limited, retry } = useAssetLoad(project, listArticles);
const articles = computed(() => data.value?.articles ?? []);
const query = computed(() => projectQuery(route.query));
// /items/article/<id>: the id is one encoded segment (ADR 0016).
const to = (id: string) => ({ path: `/items/article/${encodeURIComponent(id)}`, query: { ...query.value, from: 'articles' } });
</script>

<template>
  <h1>Articles</h1>
  <p v-if="!project" class="note">Global guidance. Choose a Project to include its articles.</p>

  <div v-if="skeleton" class="list" aria-busy="true">
    <div v-for="n in 3" :key="n" class="card bar"></div>
  </div>
  <template v-else-if="state === 'loading' || state === 'idle'" />

  <div v-else-if="state === 'error' && limited" class="box warning">
    <p role="status">
      {{ wait > 0 ? `Too many requests — try again in ${wait} s.` : 'Too many requests — you can retry now.' }}
    </p>
    <button type="button" class="retry" :disabled="wait > 0" @click="retry">Retry</button>
  </div>
  <div v-else-if="state === 'error'" class="box danger" role="alert">
    <p>{{ errorSentence(failure?.message ?? '', failure?.hint) }} {{ failure?.hint }}</p>
    <button type="button" class="retry" @click="retry">Retry</button>
  </div>

  <template v-else-if="state === 'ready'">
    <p v-if="data?.hint" class="note">{{ plain(data.hint) }}</p>
    <div v-if="!articles.length" role="status" class="box">
      <p>{{ project ? `No articles for ${plain(project)} yet.` : 'No articles yet.' }}</p>
    </div>
    <ul v-else class="list">
      <li v-for="a in articles" :key="a.id">
        <article class="card">
          <h2>
            <RouterLink :to="to(a.id)">{{ plain(a.name) }}</RouterLink>
          </h2>
          <div class="meta">
            <code>{{ plain(a.id) }}</code>
            <ScopeBadge :scope="plain(a.scope)" />
            <span class="status" :class="a.status ?? 'published'">{{ a.status ?? 'published' }}</span>
          </div>
          <p v-for="scope in a.overrides ?? []" :key="scope" class="note">
            Overrides {{ plain(a.id) }} from {{ plain(scope) }}
          </p>
        </article>
      </li>
    </ul>
  </template>
</template>

<style scoped>
.note {
  margin: 0;
  max-width: 760px;
  color: var(--fg-secondary);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}
.list {
  display: grid;
  gap: var(--space-3);
  max-width: 940px;
  margin: var(--space-4) 0 0;
  padding: 0;
  list-style: none;
}
.card {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
  padding: var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-1);
}
.bar {
  min-height: 90px;
  background: var(--surface-3);
}
.meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
code {
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
}
.status {
  padding: 2px var(--space-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
}
.status.draft {
  border-style: dashed;
}
h2 {
  margin: 0;
  font-size: var(--text-lg);
  overflow-wrap: anywhere;
}
h2 a {
  display: inline-flex;
  align-items: center;
  min-width: var(--target-min);
  min-height: var(--target-min);
  color: var(--fg-default);
}
.box {
  max-width: 940px;
  padding: var(--space-4) var(--space-6);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-2);
  color: var(--fg-secondary);
}
.box p {
  margin: 0;
}
.danger,
.warning {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
}
.danger {
  border-color: var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-ink);
}
.warning {
  border-color: var(--miss-border);
  background: var(--miss-bg);
  color: var(--miss-ink);
}
.retry {
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  border: 1px solid currentColor;
  border-radius: var(--radius-sm);
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
  font-weight: 600;
}
</style>
