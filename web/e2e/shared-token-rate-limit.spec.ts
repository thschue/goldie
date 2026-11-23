// The shared reader token under parallel workers (#678). ./run ux starts one
// server and every Playwright worker signs in with the same reader token; the
// server's limit is per token name. The UX server must therefore tolerate a
// burst well beyond the production default (20/s, burst 40), or unrelated specs
// meet a 429 they never asked for. The HTTP API is the seam: a burst of
// signed-in session checks, as parallel workers make, must all be answered.
// The 429 specs (assets-skills, assets-loops, session-errors) stub the status
// with page.route and stay independent of this limit.
import { expect, test } from '@playwright/test';

// Well above the default burst of 40, within the 2000 the UX server allows.
const burst = 150;

test('a burst of signed-in requests on the shared token is never rate limited', async ({ request }) => {
  const statuses = await Promise.all(
    Array.from({ length: burst }, async () => (await request.get('/api/v1/session')).status()),
  );
  const limited = statuses.filter((s) => s === 429).length;
  expect(limited, `of ${burst} concurrent requests, how many got 429`).toBe(0);
  expect(
    statuses.every((s) => s === 200),
    'every request is answered with the session',
  ).toBe(true);
});
