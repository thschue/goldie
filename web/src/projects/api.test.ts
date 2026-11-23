// The Project API helpers (#496): ids are one encoded segment, an error shows
// only the envelope, and a 429 carries its Retry-After.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { type ApiError, projectItems, projectOf } from './api';

const reply = (status: number, body: string, headers: Record<string, string> = {}) =>
  vi.stubGlobal('fetch', vi.fn(async () => new Response(body, { status, headers })));

afterEach(() => vi.unstubAllGlobals());

describe('projectItems', () => {
  it('requests the id as one encoded segment, with no query', async () => {
    reply(200, '{"items":[],"suppressions":[]}');
    await projectItems('github.com/acme/re?po#x');
    expect(vi.mocked(fetch).mock.calls[0][0]).toBe('/api/v1/projects/github.com%2Facme%2Fre%3Fpo%23x/items');
  });

  it('never echoes a body that is not an envelope', async () => {
    reply(502, '<pre>panic at /srv/x.go 10.0.0.7</pre>');
    expect(((await projectItems('a/b/c').catch((e) => e)) as ApiError).message).not.toMatch(/panic|srv/);
  });
});

it('projectOf takes the first project value and ignores anything else', () => {
  expect([projectOf({ project: ['a/b/c', 'x'] }), projectOf({ project: 3 }), projectOf({})]).toEqual(['a/b/c', '', '']);
});
