import { defineComponent, h } from 'vue';
import { createRouter, createWebHistory, useRoute, type RouteRecordRaw } from 'vue-router';
import ActivityView from './activity/ActivityView.vue';
import ArticleView from './articles/ArticleView.vue';
import ArticlesView from './articles/ArticlesView.vue';
import AgentAssetsNotFound from './assets/AgentAssetsNotFound.vue';
import AgentAssetsView from './assets/AgentAssetsView.vue';
import { assetTabs } from './assets/tabs';
import { installAuthGuard } from './auth/guard';
import SignInView from './auth/SignInView.vue';
import DecisionsView from './decisions/DecisionsView.vue';
import ProjectHome from './projects/ProjectHome.vue';
import { projectOf } from './projects/api';
import { queryOf } from './search/api';
import HomeView from './search/HomeView.vue';
import SourcesView from './sources/SourcesView.vue';
import EmptyView from './views/EmptyView.vue';
import NotFoundView from './views/NotFoundView.vue';

// The routes of handoff §2.2. A screen not built yet renders the empty state
// in the shell; any other path renders the not-found view. The server answers
// every extensionless path with the SPA index (internal/ui), so nothing 404s.
const pages: [path: string, title: string][] = [
  ['/items/:kind/:name', 'Item'],
];

// Home is one address: with a Project and no query (`/?project=`) it lists that Project's Items
// under the search field, otherwise it is the search (`/`, `/?q=`, `/?q=&project=`).
const Home = defineComponent(() => {
  const route = useRoute();
  return () => h(projectOf(route.query) && !queryOf(route.query) ? ProjectHome : HomeView);
});

export const routes: RouteRecordRaw[] = [
  { path: '/', component: Home, meta: { title: 'Home' } },
  // Agent assets (handoff §3.5): one route for the tabs. Matching is
  // case-sensitive and closed over the tab list, so any other tab (Loops
  // included) gets the not-found view under the same h1.
  { path: '/assets', redirect: (to) => ({ path: '/assets/skills', query: to.query }) },
  {
    path: `/assets/:tab(${assetTabs.map((t) => t.slug).join('|')})`,
    component: AgentAssetsView,
    meta: { title: 'Agent assets' },
    sensitive: true,
  },
  { path: '/assets/:rest(.*)*', component: AgentAssetsNotFound, meta: { title: 'Page not found' } },
  { path: '/articles', component: ArticlesView, meta: { title: 'Articles' } },
  { path: '/decisions', component: DecisionsView, meta: { title: 'Decisions' } },
  { path: '/ops/activity', component: ActivityView, meta: { title: 'Activity' } },
  // Sign in is public: the guard lets it through, and the shell shows no role chip.
  { path: '/signin', component: SignInView, meta: { title: 'Sign in', public: true } },
  { path: '/items/article/:name', component: ArticleView, meta: { title: 'Article' } },
  { path: '/ops/sources', component: SourcesView, meta: { title: 'Sources' } },
  ...pages.map(([path, title]): RouteRecordRaw => ({ path, component: EmptyView, meta: { title } })),
  { path: '/:pathMatch(.*)*', component: NotFoundView, meta: { title: 'Page not found' } },
];

export function createAppRouter() {
  const router = createRouter({ history: createWebHistory(), routes });
  installAuthGuard(router);
  router.afterEach((to) => {
    document.title = `${to.meta.title} · goldie`;
  });
  return router;
}
