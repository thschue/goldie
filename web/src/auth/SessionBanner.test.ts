// The session-ended banner shows on app views, never on /signin itself (#495).
// Rendered with Vue's server renderer; the browser flow is in web/e2e.
import { afterEach, describe, expect, it } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createMemoryHistory, createRouter } from 'vue-router';
import { routes } from '../router';
import SessionBanner from './SessionBanner.vue';
import { ended } from './session';

async function render(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes });
  await router.push(path);
  await router.isReady();
  const app = createSSRApp({ render: () => h(SessionBanner) });
  app.use(router);
  return renderToString(app);
}

afterEach(() => {
  ended.value = false;
});

describe('SessionBanner', () => {
  it('shows after a session ended on an app view', async () => {
    ended.value = true;
    expect(await render('/decisions')).toContain('Your session ended.');
  });

  it('is hidden on /signin, which it would link to', async () => {
    ended.value = true;
    expect(await render('/signin')).not.toContain('Your session ended');
  });

  it('is hidden while the session has not ended', async () => {
    expect(await render('/decisions')).not.toContain('Your session ended');
  });
});
