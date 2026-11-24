// The Agent assets frame at /assets/:tab (#584, handoff §3.5, A3): the h1, the
// tabs Skills, Agents, Loops and Instructions (#647), the WAI-ARIA tabs pattern,
// the side nav group, reload, and the `goldie pull --dry-run` header snippet.
// Per width and theme: the shot, axe, no horizontal scroll, targets of 44 px.
import { expect, test, type Locator, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence, problems, sideNav, withNav } from './ux';

const tabNames = ['Skills', 'Agents', 'Loops', 'Instructions'] as const;
const slugs = ['skills', 'agents', 'loops', 'instructions'] as const;

const h1 = (page: Page) => page.getByRole('heading', { level: 1 });
const tabs = (page: Page) => page.getByRole('tablist').getByRole('tab');
const tab = (page: Page, name: string) => page.getByRole('tablist').getByRole('tab', { name, exact: true });
const pathOf = (page: Page) => new URL(page.url()).pathname;

// expectOneTabStop: the named tab has focus and is the only one in the tab
// order (roving tabindex).
async function expectOneTabStop(page: Page, focused: string) {
  await expect(tab(page, focused)).toBeFocused();
  const stops = await tabs(page).evaluateAll((els) => els.map((el) => el.getAttribute('tabindex')));
  expect(stops.filter((t) => t === '0'), 'exactly one tab is in the tab order').toHaveLength(1);
  expect(stops.filter((t) => t !== '0').every((t) => t === '-1'), 'the others have tabindex -1').toBe(true);
  await expect(tab(page, focused)).toHaveAttribute('tabindex', '0');
}

eachViewport((width, theme) => {
  for (const slug of slugs) {
    test(`/assets/${slug} is captured, axe-clean, self-hosted and error-free`, async ({ page, baseURL }) => {
      const seen = await problems(page, baseURL ?? '');
      const res = await page.goto(`/assets/${slug}`);
      expect(res?.status()).toBe(200);
      await expect(h1(page)).toHaveText('Agent assets');
      await evidence(page, width, theme, '', `assets-${slug}`);
      expect(seen.external, 'requests to other origins').toEqual([]);
      expect(seen.failed, 'same-origin 4xx/5xx or failed requests').toEqual([]);
      expect(seen.errors, 'page errors and CSP violations').toEqual([]);
    });
  }

  test('an unknown tab is captured and axe-clean', async ({ page }) => {
    await page.goto('/assets/nope');
    await expect(h1(page)).toHaveText('Agent assets');
    await evidence(page, width, theme, '', 'assets-notfound');
  });
});

test.describe('routes', () => {
  test('/assets redirects to /assets/skills', async ({ page }) => {
    const res = await page.goto('/assets');
    expect(res?.status()).toBe(200);
    await expect(page).toHaveURL((u) => u.pathname === '/assets/skills');
    await expect(h1(page)).toHaveText('Agent assets');
    await expect(tab(page, 'Skills')).toHaveAttribute('aria-selected', 'true');
  });

  test('/assets keeps the ?project= query when it redirects', async ({ page }) => {
    await page.goto('/assets?project=demo');
    await expect(page).toHaveURL((u) => u.pathname === '/assets/skills' && u.searchParams.get('project') === 'demo');
  });

  for (const bad of ['nope', 'Loops', 'Skills', '%3Cscript%3E']) {
    test(`/assets/${bad} shows the not-found view under the h1, never a blank page`, async ({ page, baseURL }) => {
      const seen = await problems(page, baseURL ?? '');
      const res = await page.goto(`/assets/${bad}`);
      expect(res?.status(), 'the SPA index is served with 200').toBe(200);
      await expect(page.getByRole('banner')).toBeVisible();
      await expect(h1(page)).toHaveCount(1);
      await expect(h1(page)).toHaveText('Agent assets');
      const main = page.getByRole('main');
      await expect(main).toContainText(/not found/i);
      await expect(main.getByRole('link', { name: /home/i })).toHaveAttribute('href', '/');
      await expect(page.getByRole('tab')).toHaveCount(0);
      expect(pathOf(page), 'the address is not rewritten').toBe(`/assets/${bad}`);
      expect(seen.errors).toEqual([]);
    });
  }

  // An escaped slash is a hostile page path: the server refuses it (path
  // hardening in internal/ui) instead of serving the SPA index.
  test('/assets/skills%2Fagents is refused with a 4xx and never serves the page', async ({ request }) => {
    const res = await request.get('/assets/skills%2Fagents', { maxRedirects: 0 });
    expect([400, 404, 414], 'an escaped slash in a page path is refused').toContain(res.status());
    const body = await res.text();
    expect(body).not.toContain('id="app"');
    expect(body).not.toMatch(/Agent assets/);
  });

  test('/assets/loops shows the Loops tab, selected, between Agents and Instructions (replaces #584 "loops is not found")', async ({ page }) => {
    const res = await page.goto('/assets/loops');
    expect(res?.status()).toBe(200);
    await expect(tabs(page)).toHaveCount(4);
    await expect(tab(page, 'Loops')).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('main')).not.toContainText(/not found/i);
    expect(pathOf(page)).toBe('/assets/loops');
  });
});

test.describe('the tabs follow the WAI-ARIA tabs pattern', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('a tablist of Skills, Agents, Loops and Instructions, each tab controlling a tabpanel', async ({ page }) => {
    await page.goto('/assets/skills');
    await expect(page.getByRole('tablist')).toHaveCount(1);
    expect((await tabs(page).allTextContents()).map((t) => t.trim())).toEqual([...tabNames]);
    const ids = await tabs(page).evaluateAll((els) => els.map((el) => el.getAttribute('aria-controls')));
    for (const id of ids) expect(id, 'every tab has aria-controls').toBeTruthy();
    const selected = tab(page, 'Skills');
    await expect(selected).toHaveAttribute('aria-selected', 'true');
    const panel = page.locator(`[id="${await selected.getAttribute('aria-controls')}"]`);
    await expect(panel).toHaveAttribute('role', 'tabpanel');
    await expect(panel).toBeVisible();
    await expect(page.getByRole('tabpanel')).toHaveCount(1);
    expect(await panel.getAttribute('aria-labelledby'), 'the panel is named by its tab').toBe(await selected.getAttribute('id'));
    await expect(page.locator('[aria-selected="true"]')).toHaveCount(1);
    await expect(tab(page, 'Agents')).toHaveAttribute('aria-selected', 'false');
    await expect(tab(page, 'Loops')).toHaveAttribute('aria-selected', 'false');
    await expect(tab(page, 'Instructions')).toHaveAttribute('aria-selected', 'false');
  });

  // The Skills tab has content since #587 (web/e2e/assets-skills.spec.ts).
  for (const slug of ['agents', 'instructions']) {
    test(`the ${slug} tab panel stays empty: no content yet`, async ({ page }) => {
      await page.goto(`/assets/${slug}`);
      await expect(page.getByRole('tabpanel')).toBeVisible();
      await expect(page.getByRole('tabpanel').getByRole('link')).toHaveCount(0);
      await expect(page.getByRole('tabpanel').getByRole('table')).toHaveCount(0);
    });
  }

  for (const [i, name] of tabNames.entries()) {
    test(`the URL names the selected tab: /assets/${slugs[i]} selects ${name} and makes it the one tab stop`, async ({ page }) => {
      await page.goto(`/assets/${slugs[i]}`);
      await expect(tab(page, name)).toHaveAttribute('aria-selected', 'true');
      await expect(page.locator('[aria-selected="true"]')).toHaveCount(1);
      await expect(tab(page, name)).toHaveAttribute('tabindex', '0');
      const stops = await tabs(page).evaluateAll((els) => els.map((el) => el.getAttribute('tabindex')));
      expect(stops.filter((t) => t === '0')).toHaveLength(1);
    });
  }

  test('clicking a tab selects it, changes the URL and keeps one panel', async ({ page }) => {
    await page.goto('/assets/skills');
    await tab(page, 'Agents').click();
    await expect(page).toHaveURL((u) => u.pathname === '/assets/agents');
    await expect(tab(page, 'Agents')).toHaveAttribute('aria-selected', 'true');
    await expect(tab(page, 'Skills')).toHaveAttribute('aria-selected', 'false');
    await expect(page.getByRole('tabpanel')).toHaveCount(1);
    await expect(h1(page)).toHaveText('Agent assets');
  });

  test('Right moves focus to the next tab, wrapping from the last to the first', async ({ page }) => {
    await page.goto('/assets/skills');
    await tab(page, 'Skills').focus();
    await expectOneTabStop(page, 'Skills');
    await page.keyboard.press('ArrowRight');
    await expectOneTabStop(page, 'Agents');
    await page.keyboard.press('ArrowRight');
    await expectOneTabStop(page, 'Loops');
    await page.keyboard.press('ArrowRight');
    await expectOneTabStop(page, 'Instructions');
    await page.keyboard.press('ArrowRight');
    await expectOneTabStop(page, 'Skills');
  });

  test('Left moves focus to the previous tab, wrapping from the first to the last', async ({ page }) => {
    await page.goto('/assets/skills');
    await tab(page, 'Skills').focus();
    await page.keyboard.press('ArrowLeft');
    await expectOneTabStop(page, 'Instructions');
    await page.keyboard.press('ArrowLeft');
    await expectOneTabStop(page, 'Loops');
    await page.keyboard.press('ArrowLeft');
    await expectOneTabStop(page, 'Agents');
    await page.keyboard.press('ArrowLeft');
    await expectOneTabStop(page, 'Skills');
  });

  test('Home jumps to the first tab and End to the last', async ({ page }) => {
    await page.goto('/assets/agents');
    await tab(page, 'Agents').focus();
    await page.keyboard.press('End');
    await expectOneTabStop(page, 'Instructions');
    await page.keyboard.press('Home');
    await expectOneTabStop(page, 'Skills');
  });

  test('Home on the first tab and End on the last stay put', async ({ page }) => {
    await page.goto('/assets/skills');
    await tab(page, 'Skills').focus();
    await page.keyboard.press('Home');
    await expectOneTabStop(page, 'Skills');
    await page.keyboard.press('End');
    await page.keyboard.press('End');
    await expectOneTabStop(page, 'Instructions');
  });

  test('Tab leaves the tablist: the other tabs are not tab stops', async ({ page }) => {
    await page.goto('/assets/agents');
    await tab(page, 'Agents').focus();
    await page.keyboard.press('Tab');
    expect(await page.evaluate(() => !!document.activeElement?.closest('[role="tablist"]'))).toBe(false);
    await page.keyboard.press('Shift+Tab');
    await expect(tab(page, 'Agents')).toBeFocused();
  });

  test('the selected tab differs by more than colour', async ({ page }) => {
    await page.goto('/assets/agents');
    const cue = (l: Locator) =>
      l.evaluate((el) => {
        const s = getComputedStyle(el);
        const before = getComputedStyle(el, '::before');
        const after = getComputedStyle(el, '::after');
        return [s.fontWeight, s.textDecorationLine, s.borderBottomWidth, s.borderTopWidth, s.borderLeftWidth, s.boxShadow, before.content, after.content, s.fontStyle].join('|');
      });
    const selected = await cue(tab(page, 'Agents'));
    expect(selected, 'non-colour cue vs Skills').not.toBe(await cue(tab(page, 'Skills')));
    expect(selected, 'non-colour cue vs Loops').not.toBe(await cue(tab(page, 'Loops')));
    expect(selected, 'non-colour cue vs Instructions').not.toBe(await cue(tab(page, 'Instructions')));
  });

  test('a tab focused with the keyboard shows the 2 px focus ring with a 2 px offset', async ({ page }) => {
    await page.goto('/assets/skills');
    await tab(page, 'Skills').focus();
    await page.keyboard.press('ArrowRight');
    await expect(tab(page, 'Agents')).toBeFocused();
    const ring = await tab(page, 'Agents').evaluate((el) => {
      const s = getComputedStyle(el);
      return { width: s.outlineWidth, offset: s.outlineOffset, style: s.outlineStyle };
    });
    expect(ring).toEqual({ width: '2px', offset: '2px', style: 'solid' });
  });
});

test.describe('the side nav', () => {
  for (const width of [1280, 375]) {
    test(`lists exactly Skills, Agents, Loops and Instructions, in that order, under Agent assets at ${width} px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.goto('/assets/agents');
      const nav = await withNav(page);
      const group = nav.getByRole('list', { name: 'Agent assets' });
      expect((await group.getByRole('link').allTextContents()).map((t) => t.trim())).toEqual([...tabNames]);
      for (const [i, slug] of slugs.entries()) {
        await expect(group.getByRole('link').nth(i)).toHaveAttribute('href', `/assets/${slug}`);
      }
      await expect(group.getByRole('link', { name: 'Loops', exact: true })).toHaveAttribute('href', '/assets/loops');
      await expect(group.getByRole('link', { name: 'Agents', exact: true })).toHaveAttribute('aria-current', 'page');
    });
  }

  test('a nav link switches the tab and the selected tab follows', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/assets/skills');
    await sideNav(page).getByRole('link', { name: 'Instructions', exact: true }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/assets/instructions');
    await expect(tab(page, 'Instructions')).toHaveAttribute('aria-selected', 'true');
    await expect(h1(page)).toHaveText('Agent assets');
  });
});

test.describe('a reload', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('keeps the tab', async ({ page }) => {
    await page.goto('/assets/instructions');
    await page.reload();
    await expect(page).toHaveURL((u) => u.pathname === '/assets/instructions');
    await expect(tab(page, 'Instructions')).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('[aria-selected="true"]')).toHaveCount(1);
  });

  test('keeps the tab and the ?project= query', async ({ page }) => {
    await page.goto('/assets/agents?project=demo');
    await page.reload();
    await expect(page).toHaveURL((u) => u.pathname === '/assets/agents' && u.searchParams.get('project') === 'demo');
    await expect(tab(page, 'Agents')).toHaveAttribute('aria-selected', 'true');
  });

  test('keeps the ?project= query across a tab switch and the reload after it', async ({ page }) => {
    await page.goto('/assets/skills?project=demo');
    await tab(page, 'Agents').click();
    await expect(page).toHaveURL((u) => u.pathname === '/assets/agents');
    await page.reload();
    await expect(page).toHaveURL((u) => u.pathname === '/assets/agents' && u.searchParams.get('project') === 'demo');
    await expect(tab(page, 'Agents')).toHaveAttribute('aria-selected', 'true');
  });

  test('browser back returns to the previous tab', async ({ page }) => {
    await page.goto('/assets/skills');
    await tab(page, 'Agents').click();
    await expect(page).toHaveURL((u) => u.pathname === '/assets/agents');
    await page.goBack();
    await expect(page).toHaveURL((u) => u.pathname === '/assets/skills');
    await expect(tab(page, 'Skills')).toHaveAttribute('aria-selected', 'true');
  });
});

test.describe('the header snippet', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  for (const slug of slugs) {
    test(`/assets/${slug} shows goldie pull --dry-run with a Copy command button`, async ({ page }) => {
      await page.goto(`/assets/${slug}`);
      const main = page.getByRole('main');
      const command = main.getByText('goldie pull --dry-run', { exact: true });
      await expect(command).toBeVisible();
      // The button beside the header command; the global cards and blocks below carry their own.
      const copy = command.locator('xpath=following-sibling::button[@aria-label="Copy command"]');
      await expect(copy).toHaveCount(1);
      await expect(copy).toHaveAttribute('aria-label', 'Copy command');
      await expect(copy).toBeVisible();
    });
  }

  test('the snippet sits under the h1 and above the tabs', async ({ page }) => {
    await page.goto('/assets/skills');
    const heading = (await h1(page).boundingBox())!;
    const snippet = (await page.getByRole('main').getByText('goldie pull --dry-run', { exact: true }).boundingBox())!;
    const list = (await page.getByRole('tablist').boundingBox())!;
    expect(heading.y, 'the h1 is first').toBeLessThanOrEqual(snippet.y);
    expect(snippet.y + snippet.height, 'the snippet is above the tabs').toBeLessThanOrEqual(list.y + 1);
  });

  test('the copy button is keyboard-operable and copies exactly the command', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await page.goto('/assets/skills');
    const copy = page.getByRole('main').getByRole('button', { name: 'Copy command', exact: true });
    await copy.focus();
    await page.keyboard.press('Enter');
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe('goldie pull --dry-run');
  });

  test('the snippet is plain text, not a link', async ({ page }) => {
    await page.goto('/assets/skills');
    await expect(page.getByRole('main').getByRole('link', { name: /goldie pull/ })).toHaveCount(0);
  });
});

// A first run (#758, handoff §3.5, §5.2): `server --dev` with no registered Projects and no
// Project selected. Each tab lists what the API serves globally: its request leaves `project`
// out (an empty `?project=` answers 400 invalid project), and no tab asks for a Project.
// These run against the real server, no stubs.
const pick = /Pick a Project/;

type Call = { query: URLSearchParams; status: number };

// listCalls records the tab's list requests and the status each answered.
function listCalls(page: Page, noun: string) {
  const calls: Call[] = [];
  page.on('response', (res) => {
    const u = new URL(res.url());
    if (u.pathname === `/api/v1/${noun}`) calls.push({ query: u.searchParams, status: res.status() });
  });
  return calls;
}

function expectGlobalCalls(calls: Call[]) {
  expect(calls.length, 'the tab asked the API for its list').toBeGreaterThan(0);
  for (const c of calls) {
    expect(c.query.has('project'), 'the list request leaves project out').toBe(false);
    expect(c.status, 'the global list answers 200').toBe(200);
  }
}

eachViewport((width, theme) => {
  test('the Skills tab on a first run is captured and axe-clean', async ({ page }) => {
    await page.goto('/assets/skills');
    await expect(page.getByRole('tabpanel').getByRole('status').filter({ hasText: 'No skills yet.' })).toBeVisible();
    await evidence(page, width, theme, '-no-project', 'assets-skills');
  });

  test('the Agents tab on a first run is captured and axe-clean', async ({ page }) => {
    await page.goto('/assets/agents');
    await expect(page.getByRole('tabpanel').getByRole('heading', { level: 2, name: 'implementer', exact: true })).toBeVisible();
    await evidence(page, width, theme, '-no-project', 'assets-agents');
  });

  test('the Loops tab on a first run is captured and axe-clean', async ({ page }) => {
    await page.goto('/assets/loops');
    await expect(page.getByRole('tabpanel').getByRole('heading', { level: 2, name: 'product', exact: true })).toBeVisible();
    await evidence(page, width, theme, '-no-project', 'assets-loops');
  });

  test('the Instructions tab on a first run is captured and axe-clean', async ({ page }) => {
    await page.goto('/assets/instructions');
    await expect(page.getByRole('tabpanel').getByText('goldie instructions apply --target claude')).toBeVisible();
    await evidence(page, width, theme, '-no-project', 'assets-instructions');
  });

  test('the search panel for an empty Kind with no Project is captured and axe-clean', async ({ page }) => {
    await page.goto('/?q=x&kind=decision');
    await expect(page.getByRole('main').getByRole('status').filter({ hasText: 'No decisions yet.' })).toHaveCount(1);
    await evidence(page, width, theme, '-empty-kind-no-project', 'search');
  });
});

test.describe('a first run: no Projects registered, none selected', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('the Skills tab loads the global list without project and says "No skills yet."', async ({ page }) => {
    const calls = listCalls(page, 'skills');
    await page.goto('/assets/skills');
    const panel = page.getByRole('tabpanel');
    const empty = panel.getByRole('status').filter({ hasText: 'No skills yet.' });
    await expect(empty).toHaveCount(1);
    await expect(empty).not.toContainText('Project');
    await expect(page.getByRole('main')).not.toContainText(pick);
    await expect(panel.getByRole('alert')).toHaveCount(0);
    expectGlobalCalls(calls);
  });

  test('the Agents tab loads the global list without project and lists the global agents', async ({ page }) => {
    const calls = listCalls(page, 'agents');
    await page.goto('/assets/agents');
    const panel = page.getByRole('tabpanel');
    await expect(panel.getByRole('heading', { level: 2, name: 'implementer', exact: true })).toBeVisible();
    await expect(panel.getByRole('heading', { level: 2, name: 'reviewer', exact: true })).toBeVisible();
    await expect(page.getByRole('main')).not.toContainText(pick);
    await expect(panel.getByRole('alert')).toHaveCount(0);
    expectGlobalCalls(calls);
  });

  test('the Loops tab loads the global list without project and lists the global loops', async ({ page }) => {
    const calls = listCalls(page, 'loops');
    await page.goto('/assets/loops');
    const panel = page.getByRole('tabpanel');
    await expect(panel.getByRole('heading', { level: 2, name: 'product', exact: true })).toBeVisible();
    await expect(panel.getByRole('heading', { level: 2, name: 'delivery', exact: true })).toBeVisible();
    await expect(page.getByRole('main')).not.toContainText(pick);
    await expect(panel.getByRole('alert')).toHaveCount(0);
    expectGlobalCalls(calls);
  });

  test('the Instructions tab loads the global block without project and shows it', async ({ page }) => {
    const calls = listCalls(page, 'instructions');
    await page.goto('/assets/instructions');
    const panel = page.getByRole('tabpanel');
    await expect(panel.getByText('goldie instructions apply --target claude')).toBeVisible();
    await expect(panel.getByRole('button', { name: 'Copy digest', exact: true })).toBeVisible();
    await expect(page.getByRole('main')).not.toContainText(pick);
    await expect(panel.getByRole('alert')).toHaveCount(0);
    expectGlobalCalls(calls);
  });
});

// With Projects registered and none selected, the tab shows the global list and a note.
test.describe('Projects registered, none selected', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  const json = (route: Route, body: unknown) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
  const zero = '0'.repeat(64);
  const bodies = {
    skills: { skills: [{ name: 'deploy', scope: 'global', files: [{ path: 'SKILL.md', size: 16 }] }] },
    agents: { agents: [{ name: 'reviewer', scope: 'global', description: '' }] },
    loops: {
      loops: [{ name: 'product', scope: 'global', description: '', stop: '', dynamic: false, digest: zero, closure: { agents: [], skills: [] } }],
    },
    instructions: { block: '## Golden paths\n\nGlobal block.', digest: 'a'.repeat(64) },
  } as const;
  const shows = {
    skills: 'deploy',
    agents: 'reviewer',
    loops: 'product',
    instructions: 'goldie instructions apply --target claude',
  } as const;

  for (const noun of ['skills', 'agents', 'loops', 'instructions'] as const) {
    test(`the ${noun} tab shows the global list and the note to pick a Project`, async ({ page }) => {
      await page.route(/\/api\/v1\/projects$/, (route) => json(route, { projects: ['github.com/acme/shop'] }));
      const seen: URLSearchParams[] = [];
      await page.route(new RegExp(`/api/v1/${noun}(\\?.*)?$`), (route) => {
        seen.push(new URL(route.request().url()).searchParams);
        return json(route, bodies[noun]);
      });
      await page.goto(`/assets/${noun}`);
      const shown = noun === 'instructions' ? page.getByRole('tabpanel').getByText(shows[noun]) : page.getByRole('tabpanel').getByRole('heading', { level: 2, name: shows[noun], exact: true });
      await expect(shown).toBeVisible();
      await expect(
        page.getByRole('main').getByText(`Pick a Project from the selector to see its ${noun}.`, { exact: true }),
      ).toHaveCount(1);
      expect(seen.length).toBeGreaterThan(0);
      for (const q of seen) expect(q.has('project'), 'the list request leaves project out').toBe(false);
    });
  }
});

// The tab stays mounted when only ?project= changes: Back to the unselected view still shows the note.
test('Back from a selected Project to none shows the note to pick a Project', async ({ page }) => {
  await page.route(/\/api\/v1\/projects$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ projects: ['github.com/acme/shop'] }) }),
  );
  await page.goto('/assets/skills');
  await page.getByRole('combobox', { name: /Project/ }).click();
  await page.getByRole('option', { name: 'github.com/acme/shop' }).click();
  await expect(page).toHaveURL(/project=/);
  await expect(page.getByRole('main').getByText(/Pick a Project/)).toHaveCount(0);
  await page.goBack();
  await expect(page.getByRole('main').getByText('Pick a Project from the selector to see its skills.', { exact: true })).toHaveCount(1);
});
