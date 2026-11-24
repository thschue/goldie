// The Instructions tab of /assets/instructions (#590, handoff §3.5): the managed block per
// target from GET /api/v1/instructions?target=&project=. The API is stubbed per test
// (page.route) so every state is deterministic; the block is untrusted and is hostile here.
//
// Contract these specs pin down for the implementation:
// - the target switch is a role=group in the tabpanel with two buttons named "CLAUDE.md" and
//   "AGENTS.md", each with aria-pressed; CLAUDE.md is pressed when the URL names no target;
// - the choice is the URL's `?target=` (claude or agents) next to `?project=`, pushed on a click;
// - the digest is the exact string the API sent, in a mono face (computed font-family names
//   "mono"), with its own button named /copy digest/i;
// - the block is rendered with the article renderer (MarkdownBody, #498) inside the tabpanel;
// - the apply command is a CommandSnippet: the text `goldie instructions apply --target <t>`
//   with a "Copy command" button;
// - states follow #587: an error is a role=alert in the panel, the 429 warning is role=status,
//   the loading skeletons carry aria-busy="true".
import { expect, test, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence } from './ux';

const shop = 'github.com/acme/shop';
const apiProjects = /\/api\/v1\/projects$/;
const apiInstructions = /\/api\/v1\/instructions(\?.*)?$/;

const digestOf = (c: string) => c.repeat(64);
const claudeBlock = '## Golden paths\n\nUse **goldie** for CLAUDE.md.\n\nDecisions clause for claude.';
const agentsBlock = '## Golden paths\n\nUse **goldie** for AGENTS.md.\n\nDecisions clause for agents.';
const blocks = {
  claude: { block: claudeBlock, digest: digestOf('a') },
  agents: { block: agentsBlock, digest: digestOf('b') },
} as const;

const json = (route: Route, status: number, body: unknown, headers: Record<string, string> = {}) =>
  route.fulfill({ status, contentType: 'application/json', headers, body: JSON.stringify(body) });

const envelope = (code: string, message: string, hint: string) => ({ error: { code, message, details: { hint } } });

const stubProjects = (page: Page) => page.route(apiProjects, (route) => json(route, 200, { projects: [shop] }));

// stubBlocks answers by the target query and returns the request URLs seen.
async function stubBlocks(page: Page) {
  const seen: string[] = [];
  await page.route(apiInstructions, (route) => {
    const u = new URL(route.request().url());
    seen.push(u.pathname + u.search);
    const t = u.searchParams.get('target') as keyof typeof blocks;
    return blocks[t]
      ? json(route, 200, blocks[t])
      : json(route, 400, envelope('invalid_argument', 'Unknown target.', 'Use claude or agents.'));
  });
  return seen;
}

const panel = (page: Page) => page.getByRole('tabpanel');
const toggle = (page: Page, name: 'CLAUDE.md' | 'AGENTS.md') =>
  panel(page).getByRole('group').getByRole('button', { name, exact: true });
const open = (page: Page, query = '') => page.goto(`/assets/instructions?${query}project=${encodeURIComponent(shop)}`);
const pwned = (page: Page) => page.evaluate(() => (window as unknown as { __pwned?: string }).__pwned);

eachViewport((width, theme) => {
  test('the Instructions tab with a block is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await stubBlocks(page);
    await open(page);
    await expect(panel(page)).toContainText('Use goldie for CLAUDE.md.');
    await evidence(page, width, theme, '', 'assets-instructions');
  });

  test('the Instructions tab with the AGENTS.md block is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await stubBlocks(page);
    await open(page, 'target=agents&');
    await expect(panel(page)).toContainText('Use goldie for AGENTS.md.');
    await evidence(page, width, theme, '-agents', 'assets-instructions');
  });

  test('the Instructions tab with a hostile block is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiInstructions, (route) =>
      json(route, 200, {
        block: `<img src=x onerror=1>\n\n${'long-word-'.repeat(40)}\n\n\`\`\`\n${'x'.repeat(300)}\n\`\`\``,
        digest: digestOf('c'),
      }),
    );
    await open(page);
    await expect(panel(page)).toContainText('long-word-');
    await evidence(page, width, theme, '-long', 'assets-instructions');
  });

  test('the Instructions tab error state is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiInstructions, (route) =>
      json(route, 400, envelope('invalid_argument', 'Unknown target.', 'Use claude or agents.')),
    );
    await open(page);
    await expect(panel(page).getByRole('alert')).toBeVisible();
    await evidence(page, width, theme, '-error', 'assets-instructions');
  });

  test('the Instructions tab loading skeletons are captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiInstructions, async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 4000));
      await json(route, 200, blocks.claude).catch(() => {});
    });
    await open(page);
    await expect(panel(page).locator('[aria-busy="true"]')).toBeVisible();
    await evidence(page, width, theme, '-loading', 'assets-instructions');
  });
});

test.describe('the Instructions tab', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('requests GET /api/v1/instructions with target=claude and the Project, and shows the block', async ({ page }) => {
    await stubProjects(page);
    const seen = await stubBlocks(page);
    await open(page);
    await expect(panel(page)).toContainText('Use goldie for CLAUDE.md.');
    expect(seen).toHaveLength(1);
    const u = new URL(seen[0], 'http://x');
    expect(u.pathname).toBe('/api/v1/instructions');
    expect(u.searchParams.get('target')).toBe('claude');
    expect(u.searchParams.get('project')).toBe(shop);
  });

  test('the switch is a group of two toggle buttons, CLAUDE.md pressed by default', async ({ page }) => {
    await stubProjects(page);
    await stubBlocks(page);
    await open(page);
    await expect(panel(page).getByRole('group').getByRole('button')).toHaveCount(2);
    await expect(toggle(page, 'CLAUDE.md')).toHaveAttribute('aria-pressed', 'true');
    await expect(toggle(page, 'AGENTS.md')).toHaveAttribute('aria-pressed', 'false');
  });

  test('AGENTS.md maps to target=agents: it presses, requests the agents block and sets ?target=', async ({ page }) => {
    await stubProjects(page);
    const seen = await stubBlocks(page);
    await open(page);
    await expect(panel(page)).toContainText('Use goldie for CLAUDE.md.');
    await toggle(page, 'AGENTS.md').click();
    await expect(panel(page)).toContainText('Use goldie for AGENTS.md.');
    await expect(toggle(page, 'AGENTS.md')).toHaveAttribute('aria-pressed', 'true');
    await expect(toggle(page, 'CLAUDE.md')).toHaveAttribute('aria-pressed', 'false');
    const last = new URL(seen[seen.length - 1], 'http://x');
    expect(last.searchParams.get('target')).toBe('agents');
    expect(last.searchParams.get('project')).toBe(shop);
    const u = new URL(page.url());
    expect(u.pathname).toBe('/assets/instructions');
    expect(u.searchParams.get('target')).toBe('agents');
    expect(u.searchParams.get('project')).toBe(shop);
    await expect(panel(page)).not.toContainText('Use goldie for CLAUDE.md.');
  });

  test('?target=agents opens with AGENTS.md pressed and requests only the agents block', async ({ page }) => {
    await stubProjects(page);
    const seen = await stubBlocks(page);
    await open(page, 'target=agents&');
    await expect(panel(page)).toContainText('Use goldie for AGENTS.md.');
    await expect(toggle(page, 'AGENTS.md')).toHaveAttribute('aria-pressed', 'true');
    await expect(toggle(page, 'CLAUDE.md')).toHaveAttribute('aria-pressed', 'false');
    expect(seen.map((s) => new URL(s, 'http://x').searchParams.get('target'))).toEqual(['agents']);
  });

  test('the target survives a reload and the browser Back button', async ({ page }) => {
    await stubProjects(page);
    await stubBlocks(page);
    await open(page);
    await expect(panel(page)).toContainText('Use goldie for CLAUDE.md.');
    await toggle(page, 'AGENTS.md').click();
    await expect(panel(page)).toContainText('Use goldie for AGENTS.md.');
    await page.reload();
    await expect(toggle(page, 'AGENTS.md')).toHaveAttribute('aria-pressed', 'true');
    await expect(panel(page)).toContainText('Use goldie for AGENTS.md.');
    await page.goBack();
    await expect(toggle(page, 'CLAUDE.md')).toHaveAttribute('aria-pressed', 'true');
    await expect(panel(page)).toContainText('Use goldie for CLAUDE.md.');
  });

  test('the switch works from the keyboard', async ({ page }) => {
    await stubProjects(page);
    await stubBlocks(page);
    await open(page);
    await expect(panel(page)).toContainText('Use goldie for CLAUDE.md.');
    await toggle(page, 'AGENTS.md').focus();
    await page.keyboard.press('Enter');
    await expect(toggle(page, 'AGENTS.md')).toHaveAttribute('aria-pressed', 'true');
    await expect(panel(page)).toContainText('Use goldie for AGENTS.md.');
  });

  test('the digest is shown in full, in mono, and the copy button copies exactly it', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await stubProjects(page);
    await stubBlocks(page);
    await open(page);
    const digest = panel(page).getByText(blocks.claude.digest, { exact: true });
    await expect(digest).toBeVisible();
    const family = await digest.evaluate((el) => getComputedStyle(el).fontFamily);
    expect(family).toMatch(/mono/i);
    const copy = panel(page).getByRole('button', { name: /copy digest/i });
    await copy.focus();
    await page.keyboard.press('Enter');
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(blocks.claude.digest);
  });

  test('a long digest wraps inside the viewport and the page does not scroll sideways at 375 px', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    await stubProjects(page);
    await stubBlocks(page);
    await open(page);
    const digest = panel(page).getByText(blocks.claude.digest, { exact: true });
    await expect(digest).toBeVisible();
    const box = (await digest.boundingBox())!;
    expect(box.x + box.width, 'the digest stays inside the viewport').toBeLessThanOrEqual(375);
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test('the block is rendered as Markdown, not shown as source', async ({ page }) => {
    await stubProjects(page);
    await stubBlocks(page);
    await open(page);
    await expect(panel(page).locator('strong', { hasText: 'goldie' })).toBeVisible();
    await expect(panel(page).getByRole('heading', { name: 'Golden paths' })).toBeVisible();
    await expect(panel(page)).not.toContainText('**goldie**');
    await expect(panel(page)).not.toContainText('## Golden paths');
  });

  test('the block never adds a second h1 and no heading skips a level', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiInstructions, (route) =>
      json(route, 200, { block: '# Top\n\n### Deep\n\ntext', digest: digestOf('d') }),
    );
    await open(page);
    await expect(panel(page)).toContainText('Deep');
    const levels = await page
      .locator('main h1, main h2, main h3, main h4, main h5, main h6')
      .evaluateAll((els) => els.map((el) => Number(el.tagName.slice(1))));
    expect(levels.filter((l) => l === 1)).toHaveLength(1);
    expect(levels[0]).toBe(1);
    levels.forEach((l, i) => {
      if (i > 0) expect(l, `heading #${i + 1} follows level ${levels[i - 1]}`).toBeLessThanOrEqual(levels[i - 1] + 1);
    });
  });

  test('a hostile block renders inert: no script, no handler, no raw HTML, no unsafe link', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiInstructions, (route) =>
      json(route, 200, {
        block: [
          'Before <script>window.__pwned = "script"</script> after.',
          '',
          '<img src=x onerror="window.__pwned = \'img\'">',
          '',
          '<iframe src="https://evil.example/frame"></iframe>',
          '',
          '<style>body { display: none !important }</style>',
          '',
          '<a href="javascript:window.__pwned = \'raw\'">raw anchor</a>',
          '',
          '[md js link](javascript:window.__pwned = "md")',
          '',
          '[data link](data:text/html;base64,PHNjcmlwdD5hbGVydCg1KTwvc2NyaXB0Pg==)',
          '',
          'bidi ‮txt.exe‬ and zero​width',
          '',
          'Still **bold** after all that.',
        ].join('\n'),
        digest: digestOf('e'),
      }),
    );
    await open(page);
    await expect(panel(page).locator('strong', { hasText: 'bold' })).toBeVisible();
    expect(await panel(page).locator('script, img, iframe, style, [onerror]').count()).toBe(0);
    expect(
      await panel(page).locator('a[href^="javascript:" i], a[href^="data:" i], a[href^="vbscript:" i]').count(),
    ).toBe(0);
    await expect(page.locator('body')).toBeVisible();
    expect(await pwned(page)).toBeUndefined();
    const text = await panel(page).innerText();
    expect(text).not.toMatch(/[​-‏‪-‮⁠-⁩﻿]/);
  });

  test('a hostile digest is shown as text, never as markup', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiInstructions, (route) =>
      json(route, 200, { block: 'ok', digest: '<img src=x onerror=window.__pwned=1>' }),
    );
    await open(page);
    await expect(panel(page)).toContainText('<img src=x onerror=window.__pwned=1>');
    expect(await panel(page).locator('img').count()).toBe(0);
    expect(await pwned(page)).toBeUndefined();
  });

  test('shows the apply command for the target, copyable, and it follows the switch', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await stubProjects(page);
    await stubBlocks(page);
    await open(page);
    const claude = 'goldie instructions apply --target claude';
    await expect(panel(page).getByText(claude, { exact: true })).toBeVisible();
    const copy = panel(page).getByRole('button', { name: 'Copy command', exact: true });
    await expect(copy).toHaveCount(1);
    await copy.focus();
    await page.keyboard.press('Enter');
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(claude);
    await toggle(page, 'AGENTS.md').click();
    await expect(panel(page).getByText('goldie instructions apply --target agents', { exact: true })).toBeVisible();
    await expect(panel(page).getByText(claude, { exact: true })).toHaveCount(0);
  });

  test('switching targets quickly never lets a stale response win', async ({ page }) => {
    await stubProjects(page);
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    await page.route(apiInstructions, async (route) => {
      const t = new URL(route.request().url()).searchParams.get('target') as keyof typeof blocks;
      if (t === 'claude') await gate;
      await json(route, 200, blocks[t]).catch(() => {});
    });
    await open(page);
    await toggle(page, 'AGENTS.md').click();
    await expect(panel(page)).toContainText('Use goldie for AGENTS.md.');
    release();
    // Give the late CLAUDE.md response every chance to land.
    await page.waitForTimeout(500);
    await expect(panel(page)).toContainText('Use goldie for AGENTS.md.');
    await expect(panel(page)).not.toContainText('Use goldie for CLAUDE.md.');
    await expect(panel(page).getByText(blocks.agents.digest, { exact: true })).toBeVisible();
    await expect(panel(page).getByText(blocks.claude.digest, { exact: true })).toHaveCount(0);
    await expect(toggle(page, 'AGENTS.md')).toHaveAttribute('aria-pressed', 'true');
    expect(new URL(page.url()).searchParams.get('target')).toBe('agents');
  });

  test('switching back and forth ends on the last target, whatever order the answers arrive in', async ({ page }) => {
    await stubProjects(page);
    const releases: Record<string, () => void> = {};
    let n = 0;
    await page.route(apiInstructions, async (route) => {
      const t = new URL(route.request().url()).searchParams.get('target') as keyof typeof blocks;
      const call = n++;
      await new Promise<void>((resolve) => (releases[`${t}${call}`] = resolve));
      await json(route, 200, blocks[t]).catch(() => {});
    });
    await open(page);
    await expect.poll(() => Object.keys(releases)).toEqual(['claude0']);
    await toggle(page, 'AGENTS.md').click();
    await expect.poll(() => Object.keys(releases).length).toBe(2);
    await toggle(page, 'CLAUDE.md').click();
    await expect.poll(() => Object.keys(releases).length).toBe(3);
    // The newest answers first, then the older ones.
    releases.claude2();
    await expect(panel(page)).toContainText('Use goldie for CLAUDE.md.');
    releases.agents1();
    releases.claude0();
    await page.waitForTimeout(500);
    await expect(panel(page)).toContainText('Use goldie for CLAUDE.md.');
    await expect(panel(page)).not.toContainText('Use goldie for AGENTS.md.');
    await expect(toggle(page, 'CLAUDE.md')).toHaveAttribute('aria-pressed', 'true');
  });

  test('a 400 shows the envelope hint in the danger alert', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiInstructions, (route) =>
      json(route, 400, envelope('invalid_argument', 'Unknown target.', 'Use claude or agents.')),
    );
    await open(page);
    const alert = panel(page).getByRole('alert');
    await expect(alert).toContainText('Use claude or agents.');
    await expect(alert).toContainText("Couldn't load instructions.");
    await expect(panel(page).getByText(/goldie instructions apply/)).toHaveCount(0);
  });

  test('an error never shows more than the envelope: no stack, path or address', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiInstructions, (route) =>
      route.fulfill({
        status: 502,
        contentType: 'text/html',
        body: '<pre>panic at /srv/goldie/x.go:12 10.0.0.7:8080</pre>',
      }),
    );
    await open(page);
    const alert = panel(page).getByRole('alert');
    await expect(alert).toContainText("Couldn't load instructions.");
    await expect(alert).not.toContainText('panic');
    await expect(alert).not.toContainText('10.0.0.7');
    await expect(alert).not.toContainText('/srv/');
  });

  test('the error alert has Retry, which reloads the same target', async ({ page }) => {
    await stubProjects(page);
    let calls = 0;
    const targets: (string | null)[] = [];
    await page.route(apiInstructions, (route) => {
      calls++;
      targets.push(new URL(route.request().url()).searchParams.get('target'));
      if (calls === 1) return json(route, 500, envelope('internal', 'Something went wrong.', 'Try again in a moment.'));
      return json(route, 200, blocks.agents);
    });
    await open(page, 'target=agents&');
    const alert = panel(page).getByRole('alert');
    await expect(alert).toContainText('Try again in a moment.');
    await alert.getByRole('button', { name: 'Retry' }).click();
    await expect(panel(page)).toContainText('Use goldie for AGENTS.md.');
    await expect(panel(page).getByRole('alert')).toHaveCount(0);
    expect(targets).toEqual(['agents', 'agents']);
  });

  test('a 429 disables Retry and shows a countdown until it ends', async ({ page }) => {
    await stubProjects(page);
    let calls = 0;
    await page.route(apiInstructions, (route) => {
      calls++;
      if (calls === 1) {
        return json(route, 429, envelope('rate_limited', 'Too many requests.', 'Wait, then retry.'), {
          'Retry-After': '3',
        });
      }
      return json(route, 200, blocks.claude);
    });
    await open(page);
    await expect(panel(page)).toHaveText(/try again in [123] s/);
    const retry = panel(page).getByRole('button', { name: 'Retry' });
    await expect(retry).toBeDisabled();
    await expect(retry).toBeEnabled({ timeout: 6000 });
    await retry.click();
    await expect(panel(page)).toContainText('Use goldie for CLAUDE.md.');
  });

  test('shows skeletons with aria-busy while a slow load runs, and no spinner', async ({ page }) => {
    await stubProjects(page);
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    await page.route(apiInstructions, async (route) => {
      await gate;
      await json(route, 200, blocks.claude);
    });
    await open(page);
    await expect(panel(page).locator('[aria-busy="true"]')).toBeVisible();
    await expect(panel(page).getByText(blocks.claude.digest, { exact: true })).toHaveCount(0);
    expect(await page.locator('[role="progressbar"], [class*="spinner" i]').count(), 'no spinner').toBe(0);
    release();
    await expect(panel(page)).toContainText('Use goldie for CLAUDE.md.');
    await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
  });

  test('shows the API hint for an unregistered Project next to the block', async ({ page }) => {
    await stubProjects(page);
    const hint = 'The Project github.com/acme/none is not registered on this server, so this is global guidance only.';
    await page.route(apiInstructions, (route) => json(route, 200, { ...blocks.claude, hint }));
    await open(page);
    await expect(panel(page)).toContainText(hint);
    await expect(panel(page)).toContainText('Use goldie for CLAUDE.md.');
  });
});
