import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router';
import ProjectHome from './projects/ProjectHome.vue';
import EmptyView from './views/EmptyView.vue';
import NotFoundView from './views/NotFoundView.vue';

// The routes of handoff §2.2. A screen not built yet renders the empty state
// in the shell; any other path renders the not-found view. The server answers
// every extensionless path with the SPA index (internal/ui), so nothing 404s.
const pages: [path: string, title: string][] = [
  ['/signin', 'Sign in'],
  ['/decisions', 'Decisions'],
  ['/assets/skills', 'Skills'],
  ['/assets/agents', 'Agents'],
  ['/assets/loops', 'Loops'],
  ['/assets/instructions', 'Instructions'],
  ['/items/:kind/:name', 'Item'],
  ['/ops/activity', 'Activity'],
  ['/ops/sources', 'Sources'],
];

export const routes: RouteRecordRaw[] = [
  // Home lists a Project's Items once ?project= names one, else the empty state.
  { path: '/', component: ProjectHome, meta: { title: 'Home', heading: 'What are you deciding today?' } },
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
