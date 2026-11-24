// The journey steps per auth state (#750): every page the journeys J1 to J3 visit,
// seen as an anonymous visitor, a reader and an operator. ./run ux runs this file
// once per state, as the Playwright projects `anonymous`, `reader` and `operator`
// (web/playwright.config.ts), against the one shared server. A step the state can't
// reach asserts what it gets instead of skipping: an anonymous visitor on a
// protected page lands on /signin?next=<page>, a reader on an operator page reads
// "This page is for operators." Shots: ux/<state>/<journey>-<step>-<width>-<theme>.png,
// listed in ux/index.md.
import { expect, test, type Page } from '@playwright/test';
import { eachViewport, evidence, problems } from '../ux';

type State = 'anonymous' | 'reader' | 'operator';

interface Step {
  journey: string;
  name: string;
  path: string;
  h1: string;
  // Public: the page is the same in every state (the standalone sign-in page).
  public?: boolean;
  // Only an operator gets the page's content; a reader gets the operators-only message.
  operatorsOnly?: boolean;
  // Extra checks for a state that gets the page's content.
  reached?: (page: Page) => Promise<void>;
}

const main = (page: Page) => page.getByRole('main');
const items = (page: Page) => main(page).getByRole('list').getByRole('listitem');
const home = 'What are you deciding today?';

const steps: Step[] = [
  { journey: 'j1', name: 'signin', path: '/signin', h1: 'Sign in', public: true },
  { journey: 'j1', name: 'home', path: '/', h1: home },
  { journey: 'j2', name: 'articles', path: '/articles', h1: 'Articles' },
  { journey: 'j2', name: 'item', path: '/items/article/agent-loops', h1: 'Autonomous agent loops' },
  { journey: 'j2', name: 'hits', path: '/?q=loops', h1: home, reached: (page) => expect(items(page)).toHaveCount(1) },
  {
    journey: 'j2',
    name: 'no-hit',
    path: '/?q=zzqx',
    h1: home,
    reached: (page) => expect(main(page).getByRole('status').filter({ hasText: /No golden path/ })).toBeVisible(),
  },
  { journey: 'j3', name: 'sources', path: '/ops/sources', h1: 'Sources', operatorsOnly: true, reached: (page) => expect(items(page)).toHaveCount(1) },
  {
    journey: 'j3',
    name: 'activity',
    path: '/ops/activity',
    h1: 'Activity',
    operatorsOnly: true,
    reached: (page) => expect(main(page).getByRole('group', { name: 'Surface' })).toBeVisible(),
  },
];

const operatorsMessage = (page: Page) => main(page).getByText('This page is for operators.');

eachViewport((width, theme) => {
  for (const step of steps) {
    test(`${step.journey} ${step.name}: what this auth state gets`, async ({ page, baseURL }, info) => {
      const state = info.project.name as State;
      expect(['anonymous', 'reader', 'operator'], `the project ${state} is an auth state`).toContain(state);
      const seen = await problems(page, baseURL ?? '');

      await page.goto(step.path);
      if (state === 'anonymous' && !step.public) {
        await expect(page).toHaveURL((u) => u.pathname === '/signin' && u.searchParams.get('next') === step.path);
        await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
        await expect(page.getByRole('navigation'), 'no shell around the sign-in page').toHaveCount(0);
      } else {
        await expect(page.getByRole('heading', { level: 1 })).toHaveText(step.h1);
        if (step.operatorsOnly && state === 'reader') {
          await expect(operatorsMessage(page)).toBeVisible();
        } else {
          await expect(operatorsMessage(page)).toHaveCount(0);
          await step.reached?.(page);
        }
      }
      await evidence(page, width, theme, '', `../${state}/${step.journey}-${step.name}`);

      expect(seen.external, 'no request to another origin').toEqual([]);
      expect(seen.errors, 'no page error or CSP violation').toEqual([]);
    });
  }
});
