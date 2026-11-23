<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import { getArticle, type Article, ArticleError } from './api';
import MarkdownBody from './MarkdownBody.vue';

// The article view (#498, handoff §3.4, §5.2, §8): `/items/article/:name`, the
// name being the article id with any `/` as %2F (ADR 0016). The title is the one
// h1; the Markdown below it starts at h2. Everything from the API is printed as
// text, the Markdown through MarkdownBody. The shell keys the view by path, so
// another article is a new instance.
const route = useRoute();

const article = ref<Article | null>(null);
const error = ref<{ message: string; hint: string } | null>(null);

// A replaced_by is `<kind>:<name>` (ADR 0021) from a Source. It links only when
// it has that shape with a known Kind; anything else is not printed.
const kinds = ['article', 'skill', 'agent', 'loop'];
const replacement = computed(() => {
  const ref = article.value?.replaced_by ?? '';
  const m = /^([a-z]+):([A-Za-z0-9._/-]+)$/.exec(ref);
  if (!m || !kinds.includes(m[1])) return null;
  return { text: ref, to: `/items/${m[1]}/${encodeURIComponent(m[2])}` };
});

const overrides = computed(() => article.value?.overrides ?? []);

onMounted(async () => {
  try {
    article.value = await getArticle(String(route.params.name));
    document.title = `${article.value.name} · goldie`;
  } catch (e) {
    error.value =
      e instanceof ArticleError ? { message: e.message, hint: e.hint } : { message: "Couldn't load this article. Try again in a moment.", hint: '' };
  }
});
</script>

<template>
  <!-- Copy: handoff §8, lifecycle banners. -->
  <div v-if="article?.status === 'draft'" role="status" class="banner draft">
    <strong>Draft preview.</strong> Agents and API consumers can’t see this yet. Merge with <code>status: published</code> to release it.
  </div>
  <div v-else-if="article?.status === 'deprecated'" role="status" class="banner deprecated">
    <strong>Deprecated.</strong> Still served to agents with a warning.
    <template v-if="replacement">
      Use <RouterLink :to="replacement.to">{{ replacement.text }}</RouterLink> instead.
    </template>
  </div>

  <p v-if="!article && !error" role="status" class="loading">Loading the article…</p>
  <h1>{{ article ? article.name : error ? 'Article not available' : 'Loading…' }}</h1>

  <template v-if="article">
    <div class="meta">
      <code class="id">{{ article.id }}</code>
      <span v-if="article.scope" class="chip">{{ article.scope }}</span>
      <span v-if="article.enforced === true" class="chip enforced">Enforced</span>
    </div>
    <p v-if="overrides.length" class="overrides">
      Overrides article:{{ article.name }} <template v-for="(scope, i) in overrides" :key="scope">{{ i ? ', ' : '' }}from {{ scope }}</template>
    </p>
    <p v-if="article.keywords.length" class="keywords">
      <span v-for="k in article.keywords" :key="k" class="chip">{{ k }}</span>
    </p>
    <MarkdownBody :source="article.content" />
  </template>

  <template v-else-if="error">
    <div role="alert" class="alert">
      {{ error.message }} <span v-if="error.hint">{{ error.hint }}</span>
    </div>
    <RouterLink to="/" class="back">← Back to search</RouterLink>
  </template>
</template>

<style scoped>
.banner {
  max-width: 760px;
  margin-bottom: var(--space-4);
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--surface-2);
  color: var(--fg-default);
  font-size: var(--text-md);
}
.draft {
  border-style: dashed;
}
.deprecated {
  border-color: var(--miss-border);
  background: var(--miss-bg);
  color: var(--miss-ink);
}
.deprecated a {
  color: inherit;
  font-weight: 600;
}
.meta,
.keywords {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin: 0 0 var(--space-4);
}
.id {
  font-family: ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace;
  font-size: var(--text-md);
  color: var(--fg-secondary);
  overflow-wrap: anywhere;
}
.chip {
  padding: 0 var(--space-2);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  color: var(--fg-secondary);
  font-size: var(--text-sm);
}
.enforced {
  border-color: var(--miss-border);
  background: var(--miss-bg);
  color: var(--miss-ink);
  font-weight: 600;
}
.overrides {
  margin: 0 0 var(--space-4);
  color: var(--fg-secondary);
  font-size: var(--text-md);
}
.loading {
  margin: 0 0 var(--space-4);
  color: var(--fg-secondary);
}
.alert {
  max-width: 760px;
  margin-bottom: var(--space-4);
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--danger-border);
  border-radius: var(--radius-md);
  background: var(--danger-bg);
  color: var(--danger-ink);
}
.back {
  display: inline-flex;
  align-items: center;
  min-height: var(--target-min);
  font-weight: 600;
}
</style>
