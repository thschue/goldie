import { defineComponent, h } from 'vue';
import { createRouter, createWebHistory, useRoute, type RouteRecordRaw } from 'vue-router';
import ActivityView from './activity/ActivityView.vue';
import AgentAssetsNotFound from './assets/AgentAssetsNotFound.vue';
import AgentAssetsView from './assets/AgentAssetsView.vue';
import { assetTabs } from './assets/tabs';
import { installAuthGuard } from './auth/guard';
import SignInView from './auth/SignInView.vue';
import ProjectHome from './projects/ProjectHome.vue';
import { projectOf } from './projects/api';
import HomeView from './search/HomeView.vue';
import EmptyView from './views/EmptyView.vue';
import NotFoundView from './views/NotFoundView.vue';

// The routes of handoff §2.2. A screen not built yet renders the empty state
// in the shell; any other path renders the not-found view. The server answers
// every extensionless path with the SPA index (internal/ui), so nothing 404s.
const pages: [path: string, title: string][] = [
  ['/decisions', 'Decisions'],
  ['/items/:kind/:name', 'Item'],
  ['/ops/sources', 'Sources'],
];

// Home is one address: with a Project (`/?project=`) it lists that Project's Items,
// otherwise it is the search (`/`, `/?q=`).
const Home = defineComponent(() => {
  const route = useRoute();
  return () => h(projectOf(route.query) ? ProjectHome : HomeView);
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
  { path: '/ops/activity', component: ActivityView, meta: { title: 'Activity' } },
  // Sign in is public: the guard lets it through, and the shell shows no role chip.
  { path: '/signin', component: SignInView, meta: { title: 'Sign in', public: true } },
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
