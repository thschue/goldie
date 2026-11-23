import type { Router } from 'vue-router';
import { refresh } from './session';

// The auth guard (handoff §5.1): every route but the public ones asks the
// session first. No session sends the visitor to /signin?next=<route>; a
// session that was good and now answers 401 keeps the view and shows the
// banner (refresh marks it and still reports the session it knew).
export function installAuthGuard(router: Router) {
  router.beforeEach(async (to) => {
    if (to.meta.public || (await refresh())) return true;
    return { path: '/signin', query: { next: to.fullPath }, replace: true };
  });
}
