import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router';
import AgentAssetsNotFound from './assets/AgentAssetsNotFound.vue';
import AgentAssetsView from './assets/AgentAssetsView.vue';
import { assetTabs } from './assets/tabs';
import EmptyView from './views/EmptyView.vue';
import NotFoundView from './views/NotFoundView.vue';

// The routes of handoff §2.2. A screen not built yet renders the empty state
// in the shell; any other path renders the not-found view. The server answers
// every extensionless path with the SPA index (internal/ui), so nothing 404s.
const pages: [path: string, title: string][] = [
  ['/signin', 'Sign in'],
  ['/decisions', 'Decisions'],
  ['/items/:kind/:name', 'Item'],
  ['/ops/activity', 'Activity'],
  ['/ops/sources', 'Sources'],
];

export const routes: RouteRecordRaw[] = [
  { path: '/', component: EmptyView, meta: { title: 'Home', heading: 'What are you deciding today?' } },
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
  { path: '/assets/:rest(.*)*', component: AgentAssetsNotFound, meta: { title: 'Agent assets' } },
  ...pages.map(([path, title]): RouteRecordRaw => ({ path, component: EmptyView, meta: { title } })),
  { path: '/:pathMatch(.*)*', component: NotFoundView, meta: { title: 'Page not found' } },
];

export function createAppRouter() {
  const router = createRouter({ history: createWebHistory(), routes });
  router.afterEach((to) => {
    document.title = `${to.meta.title} · goldie`;
  });
  return router;
}
