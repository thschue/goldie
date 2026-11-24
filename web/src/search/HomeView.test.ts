// Home without a query (#721, handoff §3.2 items 1, 2 and 4): the suggestion chips, the Browse
// tiles, the agent checklist, and no placeholder. Rendered with Vue's server renderer with the API
// stubbed through fetch; the counts, the clicks and the ticks are in web/e2e/home.spec.ts.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createMemoryHistory, createRouter } from 'vue-router';
import HomeView from './HomeView.vue';

const lists = (url: string) => (url.includes('/skills') ? { skills: [] } : url.includes('/agents') ? { agents: [] } : { loops: [] });

function stubApi(answer: (url: string) => Response | Promise<Response> = (url) => new Response(JSON.stringify(lists(url)))) {
  const fetchMock = vi.fn(async (input: unknown) => answer(String(input)));
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

const requested = (fetchMock: ReturnType<typeof stubApi>) => fetchMock.mock.calls.map(([u]) => new URL(String(u), 'file:///').pathname);

async function render(path = '/') {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] });
  await router.push(path);
  await router.isReady();
  const app = createSSRApp({ render: () => h(HomeView) });
  app.use(router);
  const html = (await renderToString(app)).replace(/<!--[\s\S]*?-->/g, '');
  // Let the stubbed lists settle: a failing list must not surface as an unhandled rejection.
  await new Promise((r) => setTimeout(r, 0));
  return html;
}

const text = (html: string) => html.replace(/<[^>]+>/g, ' ').replace(/&#39;/g, "'").replace(/\s+/g, ' ');

afterEach(() => {
  vi.unstubAllGlobals();
  Reflect.deleteProperty(globalThis, 'localStorage');
});

describe('Home without a query', () => {
  it('no longer shows the "Nothing to show yet" placeholder, and keeps the question and the search field', async () => {
    stubApi();
    const html = await render();
    expect(text(html)).not.toContain('Nothing to show yet');
    expect(text(html)).not.toContain('pages come online');
    expect(html).toContain('What are you deciding today?');
    expect(html).toContain('type="search"');
  });

  it('shows the suggestion chips "Try: database · logging · retries · feature flags" in that order', async () => {
    stubApi();
    const t = text(await render());
    const at = ['Try:', 'database', 'logging', 'retries', 'feature flags'].map((w) => t.indexOf(w, t.indexOf('Try:')));
    expect(at.every((i) => i >= 0), `chips in "${t}"`).toBe(true);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
  });

  it('shows Browse the golden path with a tile per Kind, linking to /assets/<tab>, and no Articles tile', async () => {
    stubApi();
    const html = await render();
    expect(text(html)).toContain('Browse the golden path');
    for (const [name, tab] of [['Skills', 'skills'], ['Agents', 'agents'], ['Loops', 'loops']]) {
      const tile = new RegExp(`<a [^>]*href="/assets/${tab}"[^>]*>([\\s\\S]*?)</a>`).exec(html);
      expect(tile, `a tile linking to /assets/${tab}`).not.toBeNull();
      const words = text(tile![1]).trim();
      expect(words).toContain(name);
      // A one-line purpose follows the name.
      expect(words.length, `purpose in "${words}"`).toBeGreaterThan(name.length + 10);
    }
    expect(html).not.toContain('href="/assets/articles"');
  });

  it('asks GET /api/v1/skills, /agents and /loops once each, and no search', async () => {
    const fetchMock = stubApi();
    await render();
    expect(requested(fetchMock).sort()).toEqual(['/api/v1/agents', '/api/v1/loops', '/api/v1/skills']);
  });

  it('still renders every tile, the checklist and the field when every list fails', async () => {
    for (const answer of [
      () => Promise.reject(new TypeError('connect ECONNREFUSED 10.0.0.7:8080')),
      () => new Response(JSON.stringify({ error: { code: 'internal', message: 'boom', details: {} } }), { status: 500 }),
    ]) {
      stubApi(answer);
      const html = await render();
      for (const tab of ['skills', 'agents', 'loops']) expect(html).toContain(`href="/assets/${tab}"`);
      expect(html).toContain('goldie init');
      expect(html).toContain('type="search"');
      expect(html).not.toContain('role="alert"');
      expect(text(html)).not.toMatch(/10\.0\.0\.7|ECONNREFUSED/);
    }
  });

  it('shows the three checklist steps, each with a copy button and a tick', async () => {
    stubApi();
    const html = await render();
    expect(text(html)).toContain('Put your agent on the golden path');
    const t = text(html);
    const steps = ['goldie init', 'claude mcp add --transport http goldie', 'goldie instructions apply'].map((s) => t.indexOf(s));
    expect(steps.every((i) => i >= 0), `steps in "${t}"`).toBe(true);
    expect([...steps].sort((a, b) => a - b)).toEqual(steps);
    expect(html.match(/aria-label="Copy command"/g)).toHaveLength(3);
    expect(html.match(/aria-pressed="false"/g)).toHaveLength(3);
  });

  it('still renders the checklist, unticked, when localStorage throws on every access', async () => {
    stubApi();
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      get() {
        throw new DOMException('The operation is insecure.', 'SecurityError');
      },
    });
    const html = await render();
    expect(html).toContain('goldie instructions apply');
    expect(html).not.toContain('aria-pressed="true"');
  });
});

// The search call (#730): `q`, `project` when one is selected, `kind` when a Kind pill is pressed,
// `drafts` never. The request is what the API sees; the hits, the hint and "no query, no search" are in web/e2e/search.spec.ts.
describe('the search request', () => {
  const searches = (fetchMock: ReturnType<typeof stubApi>) =>
    fetchMock.mock.calls.map(([u]) => new URL(String(u), 'file:///')).filter((u) => u.pathname === '/api/v1/search');
  const answer = (url: string) => new Response(JSON.stringify(url.includes('/search') ? { query: 'x', results: [] } : lists(url)));

  it('sends q alone when no Project and no Kind is selected', async () => {
    const fetchMock = stubApi(answer);
    await render('/?q=database');
    const [call, ...more] = searches(fetchMock);
    expect(more).toEqual([]);
    expect([...call.searchParams.keys()]).toEqual(['q']);
    expect(call.searchParams.get('q')).toBe('database');
  });

  it('sends project as one encoded value when a Project is selected', async () => {
    const fetchMock = stubApi(answer);
    await render(`/?q=database&project=${encodeURIComponent('github.com/acme/re&po#x?kind=skill')}`);
    const [call] = searches(fetchMock);
    expect(call.searchParams.getAll('project')).toEqual(['github.com/acme/re&po#x?kind=skill']);
    expect(call.searchParams.has('kind')).toBe(false);
    expect(call.hash).toBe('');
  });

  it('sends kind when the address has one, and with it the Project', async () => {
    const fetchMock = stubApi(answer);
    await render('/?q=database&kind=skill&project=github.com%2Facme%2Fshop');
    const [call] = searches(fetchMock);
    expect(call.searchParams.getAll('kind')).toEqual(['skill']);
    expect(call.searchParams.getAll('project')).toEqual(['github.com/acme/shop']);
    expect(call.searchParams.get('q')).toBe('database');
  });

  it('never sends drafts, whatever the address says', async () => {
    const fetchMock = stubApi(answer);
    await render('/?q=database&drafts=true&project=github.com%2Facme%2Fshop');
    const [call] = searches(fetchMock);
    expect(call.searchParams.has('drafts')).toBe(false);
  });
});
