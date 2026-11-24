// AppShell's role variants (#166, handoff §2.1, §5.3): readers never see the
// Operate group, operators do. Rendered with Vue's server renderer, so the
// test needs no DOM emulation; the browser behaviour is in web/e2e.
import { describe, expect, it } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createMemoryHistory, createRouter } from 'vue-router';
import { routes } from '../router';
import AppShell from './AppShell.vue';

const readerNote = "Reader view: Activity and Sources are for operators and aren't shown.";

async function render(props: { role?: 'reader' | 'operator' }, path = '/') {
  const router = createRouter({ history: createMemoryHistory(), routes });
  await router.push(path);
  await router.isReady();
  const app = createSSRApp({ render: () => h(AppShell, props) });
  app.use(router);
  // Without the server renderer's fragment markers (<!--[-->), and with the
  // apostrophe it escapes as &#39; decoded.
  const html = (await renderToString(app)).replace(/<!--[\s\S]*?-->/g, '').replaceAll('&#39;', "'");
  const nav = html.slice(html.indexOf('<nav'), html.indexOf('</nav>'));
  const links = [...nav.matchAll(/<a [^>]*href="([^"]+)"[^>]*>([^<]*)<\/a>/g)].map(([, href, name]) => `${name} ${href}`);
  return { nav, links };
}

const readerLinks = [
  'Home /',
  'Articles /articles',
  'Decisions /decisions',
  'Skills /assets/skills',
  'Agents /assets/agents',
  'Loops /assets/loops',
  'Instructions /assets/instructions',
];

describe('AppShell', () => {
  it('is the reader variant by default: no Operate group, and the Reader nav note', async () => {
    const { nav, links } = await render({});
    expect(links).toEqual(readerLinks);
    expect(nav).not.toContain('Operate');
    expect(nav).toContain(readerNote);
  });

  it('shows the reader variant for role reader', async () => {
    const { links } = await render({ role: 'reader' });
    expect(links).toEqual(readerLinks);
  });

  it('adds the Operate group with Activity and Sources for role operator, without the Reader nav note', async () => {
    const { nav, links } = await render({ role: 'operator' });
    expect(links).toEqual([...readerLinks, 'Activity /ops/activity', 'Sources /ops/sources']);
    expect(nav).toMatch(/<p[^>]*>Operate<\/p>/);
    expect(nav).not.toContain('Reader view');
  });

  it('marks the current route with aria-current="page" on that link only', async () => {
    const { nav } = await render({ role: 'operator' });
    expect(nav.match(/aria-current="page"/g)).toHaveLength(1);
    expect(nav).toMatch(/<a [^>]*href="\/"[^>]*aria-current="page"|<a [^>]*aria-current="page"[^>]*href="\/"/);
  });

  it('marks only the link of the current route, and Home is not current on another route', async () => {
    const { nav } = await render({ role: 'operator' }, '/assets/skills');
    expect(nav.match(/aria-current="page"/g)).toHaveLength(1);
    expect(nav).toMatch(/<a [^>]*href="\/assets\/skills"[^>]*aria-current="page"|<a [^>]*aria-current="page"[^>]*href="\/assets\/skills"/);
    expect(nav).not.toMatch(/<a [^>]*href="\/"[^>]*aria-current="page"/);
  });

  it('is a labelled navigation landmark', async () => {
    const { nav } = await render({});
    expect(nav).toMatch(/^<nav [^>]*aria-label="[^"]+"/);
  });

  it('renders no Operate links for the reader even on an operator route', async () => {
    const { nav, links } = await render({ role: 'reader' }, '/ops/activity');
    expect(links).toEqual(readerLinks);
    expect(nav).not.toContain('/ops/');
  });

  // #812: Articles sits between Home and Decisions, and carries the Project like every entry.
  it('has an Articles entry between Home and Decisions that links to /articles', async () => {
    const { links } = await render({});
    expect(links.slice(0, 3)).toEqual(['Home /', 'Articles /articles', 'Decisions /decisions']);
  });

  it('keeps the selected Project on the Articles entry', async () => {
    const shop = 'github.com/acme/shop';
    const { links } = await render({}, `/?project=${encodeURIComponent(shop)}`);
    const href = links.find((l) => l.startsWith('Articles '))?.slice('Articles '.length) ?? '';
    const u = new URL(href, import.meta.url);
    expect(u.pathname).toBe('/articles');
    expect(u.searchParams.get('project')).toBe(shop);
  });

  const current = (nav: string, href: string) =>
    new RegExp(`<a [^>]*href="${href}"[^>]*aria-current="page"|<a [^>]*aria-current="page"[^>]*href="${href}"`).test(nav);

  it('marks Articles current on /articles, and on it only', async () => {
    const { nav } = await render({}, '/articles');
    expect(nav.match(/aria-current="page"/g)).toHaveLength(1);
    expect(current(nav, '/articles')).toBe(true);
  });

  it('marks Articles current on an article page, and on it only', async () => {
    const { nav } = await render({}, '/items/article/platform%2Fdeploy');
    expect(nav.match(/aria-current="page"/g)).toHaveLength(1);
    expect(current(nav, '/articles')).toBe(true);
  });
});
