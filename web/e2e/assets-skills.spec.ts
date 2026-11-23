// The Skills tab of /assets/skills (#587, handoff §3.5, §5.1, §5.2): a card per
// skill for the selected Project from GET /api/v1/skills?project=. The API is
// stubbed per test (page.route) so every state is deterministic.
//
// Contract these specs pin down for the implementation:
// - the cards are role=listitem inside the role=tabpanel; a card name is a
//   heading (level 2, so the heading order stays valid under the h1);
// - an error is a role=alert in the panel, the 429 warning and the empty panel
//   are role=status, both in the panel;
// - the loading skeletons carry aria-busy="true" inside the panel.
import { expect, test, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence } from './ux';

const shop = 'github.com/acme/shop';
const apiProjects = /\/api\/v1\/projects$/;
const apiSkills = /\/api\/v1\/skills(\?.*)?$/;

type Skill = { name: string; scope?: string; description?: string; requires_sync?: boolean };

const skill = (s: Skill) => ({ files: [{ path: 'SKILL.md', size: 16 }], ...s });

const json = (route: Route, status: number, body: unknown, headers: Record<string, string> = {}) =>
  route.fulfill({ status, contentType: 'application/json', headers, body: JSON.stringify(body) });

const envelope = (code: string, message: string, hint: string) => ({ error: { code, message, details: { hint } } });

const stubProjects = (page: Page) => page.route(apiProjects, (route) => json(route, 200, { projects: [shop] }));

const stubSkills = (page: Page, skills: Skill[], extra: Record<string, unknown> = {}) =>
  page.route(apiSkills, (route) => json(route, 200, { skills: skills.map(skill), ...extra }));

const panel = (page: Page) => page.getByRole('tabpanel');
const cards = (page: Page) => panel(page).getByRole('listitem');
const open = (page: Page, project = shop) => page.goto(`/assets/skills?project=${encodeURIComponent(project)}`);

const pullLine = "Contains scripts, so agents won't run it from text. Run goldie pull to install it.";

const sample: Skill[] = [
  { name: 'deploy', scope: 'global', description: 'Deploys the app to Kubernetes.', requires_sync: true },
  { name: 'audit', scope: shop, description: 'Audits a change before merge.', requires_sync: false },
  { name: 'build', description: 'Builds the image.' },
];

eachViewport((width, theme) => {
  test('the Skills tab with cards is captured and axe-clean, with a valid heading order', async ({ page }) => {
    await stubProjects(page);
    await stubSkills(page, sample);
    await open(page);
    await expect(cards(page)).toHaveCount(3);
    await evidence(page, width, theme, '', 'assets-skills');
  });

  test('the Skills tab error state is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiSkills, (route) =>
      json(route, 500, envelope('internal', 'Something went wrong.', 'Try again in a moment.')),
    );
    await open(page);
    await expect(panel(page).getByRole('alert')).toBeVisible();
    await evidence(page, width, theme, '-error', 'assets-skills');
  });

  test('the Skills tab loading skeletons are captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiSkills, async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 4000));
      await json(route, 200, { skills: [] }).catch(() => {});
    });
    await open(page);
    await expect(panel(page).locator('[aria-busy="true"]')).toBeVisible();
    await evidence(page, width, theme, '-loading', 'assets-skills');
  });

  test('the Skills tab empty state is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await stubSkills(page, []);
    await open(page);
    await expect(panel(page).getByRole('status')).toBeVisible();
    await evidence(page, width, theme, '-empty', 'assets-skills');
  });
});

test.describe('the Skills tab', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('requests GET /api/v1/skills with the Project as the project query', async ({ page }) => {
    await stubProjects(page);
    const urls: string[] = [];
    await page.route(apiSkills, (route) => {
      urls.push(route.request().url());
      return json(route, 200, { skills: [] });
    });
    await open(page);
    await expect.poll(() => urls.length).toBeGreaterThan(0);
    const u = new URL(urls[0]);
    expect(u.pathname).toBe('/api/v1/skills');
    expect(u.searchParams.get('project')).toBe(shop);
  });

  test('sorts the cards by name, ascending, whatever order the API answers in, with no sort control', async ({ page }) => {
    await stubProjects(page);
    await stubSkills(page, sample);
    await open(page);
    await expect(cards(page)).toHaveCount(3);
    await expect(cards(page).getByRole('heading')).toHaveText(['audit', 'build', 'deploy']);
    await expect(panel(page).getByRole('combobox')).toHaveCount(0);
    await expect(panel(page).getByRole('button', { name: /sort/i })).toHaveCount(0);
    await expect(panel(page)).not.toContainText(/sort/i);
  });

  test('a card shows the name, Scope, description and the MCP prompt name goldie-<name> in mono', async ({ page }) => {
    await stubProjects(page);
    await stubSkills(page, sample);
    await open(page);
    const card = cards(page).filter({ hasText: 'deploy' }).first();
    await expect(card.getByRole('heading', { name: 'deploy', exact: true })).toBeVisible();
    await expect(card).toContainText('global');
    await expect(card).toContainText('Deploys the app to Kubernetes.');
    const prompt = card.getByText('goldie-deploy', { exact: true });
    await expect(prompt).toBeVisible();
    expect(await prompt.evaluate((el) => getComputedStyle(el).fontFamily)).toMatch(/mono/i);
    await expect(panel(page)).not.toContainText('/goldie:');
    await expect(cards(page).filter({ hasText: 'audit' })).toContainText(shop);
  });

  test('only a script skill shows the requires_sync badge and the pull instruction', async ({ page }) => {
    await stubProjects(page);
    await stubSkills(page, sample);
    await open(page);
    const script = cards(page).filter({ hasText: 'deploy' });
    await expect(script).toContainText('requires_sync');
    expect((await script.innerText()).replace(/\s+/g, ' ')).toContain(pullLine);
    for (const name of ['audit', 'build']) {
      const text = await cards(page).filter({ hasText: name }).first().innerText();
      expect(text, `${name} is a text skill`).not.toMatch(/requires_sync|goldie pull|scripts/);
    }
    await expect(panel(page).getByText('requires_sync')).toHaveCount(1);
  });

  test('a card shows no usage figure and no "not used in 30 days" badge (the MVP cut)', async ({ page }) => {
    await stubProjects(page);
    await stubSkills(page, sample);
    await open(page);
    await expect(cards(page)).toHaveCount(3);
    await expect(panel(page)).not.toContainText(/uses|used|30\s?d|30 days|usage/i);
  });

  test('with no scope the card shows no Scope chip', async ({ page }) => {
    await stubProjects(page);
    await stubSkills(page, sample);
    await open(page);
    const text = await cards(page).filter({ hasText: 'build' }).first().innerText();
    expect(text.replace(/\s+/g, ' ').trim()).toBe('build Builds the image. goldie-build');
  });

  test('renders a hostile description as text, never as markup', async ({ page }) => {
    await stubProjects(page);
    await stubSkills(page, [
      { name: 'a-img', description: '<img src=x onerror=window.__pwn=1>' },
      { name: 'b-script', description: '<script>window.__pwn=2</script>' },
      { name: 'c-link', description: '[click](javascript:window.__pwn=3)' },
      { name: 'd-bidi', description: 'safe‮txt.exe​' },
    ]);
    await open(page);
    await expect(cards(page)).toHaveCount(4);
    await expect(cards(page).nth(0)).toContainText('<img src=x onerror=window.__pwn=1>');
    await expect(cards(page).nth(1)).toContainText('<script>window.__pwn=2</script>');
    await expect(cards(page).nth(2)).toContainText('[click](javascript:window.__pwn=3)');
    expect(await panel(page).locator('img, script, a[href^="javascript:" i]').count()).toBe(0);
    expect(await page.evaluate(() => (window as unknown as { __pwn?: number }).__pwn)).toBeUndefined();
  });

  test('an unregistered Project shows the API hint', async ({ page }) => {
    await stubProjects(page);
    const hint = 'The Project github.com/acme/none is not registered on this server, so this is global guidance only.';
    await stubSkills(page, sample, { hint });
    await open(page, 'github.com/acme/none');
    await expect(panel(page)).toContainText(hint);
    await expect(cards(page)).toHaveCount(3);
  });

  test('an empty list shows the neutral status panel with a link to the Source docs, not an alert', async ({ page }) => {
    await stubProjects(page);
    await stubSkills(page, []);
    await open(page);
    const status = panel(page).getByRole('status');
    await expect(status).toContainText(`No skills for ${shop} yet.`);
    await expect(status.getByRole('link', { name: /source/i })).toHaveAttribute('href', /.+/);
    await expect(panel(page).getByRole('alert')).toHaveCount(0);
    await expect(cards(page)).toHaveCount(0);
  });

  test('shows skeletons with aria-busy while a slow load runs, and no spinner', async ({ page }) => {
    await stubProjects(page);
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    await page.route(apiSkills, async (route) => {
      await gate;
      await json(route, 200, { skills: sample.map(skill) });
    });
    await open(page);
    await expect(panel(page).locator('[aria-busy="true"]')).toBeVisible();
    await expect(cards(page)).toHaveCount(0);
    expect(await page.locator('[role="progressbar"], [class*="spinner" i]').count(), 'no spinner').toBe(0);
    release();
    await expect(cards(page)).toHaveCount(3);
    await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
  });

  test('shows no skeleton at all for a load under 300 ms', async ({ page }) => {
    await stubProjects(page);
    await stubSkills(page, sample);
    await page.addInitScript(() => {
      const w = window as unknown as { __busy: boolean };
      w.__busy = false;
      new MutationObserver(() => {
        if (document.querySelector('[role="tabpanel"] [aria-busy="true"]')) w.__busy = true;
      }).observe(document, { subtree: true, childList: true, attributes: true });
    });
    await open(page);
    await expect(cards(page)).toHaveCount(3);
    expect(await page.evaluate(() => (window as unknown as { __busy: boolean }).__busy), 'a skeleton flashed').toBe(false);
  });

  test('an API error shows the danger alert with the first hint and Retry, which reloads', async ({ page }) => {
    await stubProjects(page);
    let calls = 0;
    await page.route(apiSkills, (route) => {
      calls++;
      if (calls === 1) return json(route, 500, envelope('internal', 'Something went wrong.', 'Try again in a moment.'));
      return json(route, 200, { skills: sample.map(skill) });
    });
    await open(page);
    const alert = panel(page).getByRole('alert');
    await expect(alert).toContainText("Couldn't load skills.");
    await expect(alert).toContainText('Try again in a moment.');
    await expect(cards(page)).toHaveCount(0);
    await alert.getByRole('button', { name: 'Retry' }).click();
    await expect(cards(page)).toHaveCount(3);
    await expect(panel(page).getByRole('alert')).toHaveCount(0);
  });

  test('an error never shows more than the envelope: no stack, path or address', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiSkills, (route) =>
      route.fulfill({ status: 502, contentType: 'text/html', body: '<pre>panic at /srv/goldie/x.go:12 10.0.0.7:8080</pre>' }),
    );
    await open(page);
    const alert = panel(page).getByRole('alert');
    await expect(alert).toContainText("Couldn't load skills.");
    await expect(alert).not.toContainText('panic');
    await expect(alert).not.toContainText('10.0.0.7');
    await expect(alert).not.toContainText('/srv/');
  });

  test('a 429 disables Retry and shows a countdown until it ends', async ({ page }) => {
    await stubProjects(page);
    let calls = 0;
    await page.route(apiSkills, (route) => {
      calls++;
      if (calls === 1) {
        return json(route, 429, envelope('rate_limited', 'Too many requests.', 'Wait, then retry.'), { 'Retry-After': '3' });
      }
      return json(route, 200, { skills: sample.map(skill) });
    });
    await open(page);
    await expect(panel(page)).toHaveText(/try again in [123] s/);
    const retry = panel(page).getByRole('button', { name: 'Retry' });
    await expect(retry).toBeDisabled();
    await expect(retry).toBeEnabled({ timeout: 6000 });
    await retry.click();
    await expect(cards(page)).toHaveCount(3);
  });

  test('the headings under the h1 never skip a level', async ({ page }) => {
    await stubProjects(page);
    await stubSkills(page, sample);
    await open(page);
    await expect(cards(page)).toHaveCount(3);
    const levels = await page
      .locator('main h1, main h2, main h3, main h4, main h5, main h6')
      .evaluateAll((els) => els.map((el) => Number(el.tagName.slice(1))));
    expect(levels[0], 'the h1 is first').toBe(1);
    levels.forEach((l, i) => {
      if (i > 0) expect(l, `heading #${i + 1} follows level ${levels[i - 1]}`).toBeLessThanOrEqual(levels[i - 1] + 1);
    });
  });
});
