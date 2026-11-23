// The Project API helpers (#496): ids are one encoded segment, an error shows
// only the envelope, and a 429 carries its Retry-After.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, projectItems, projectOf } from './api';

const reply = (status: number, body: string, headers: Record<string, string> = {}) =>
  vi.stubGlobal('fetch', vi.fn(async () => new Response(body, { status, headers })));

afterEach(() => vi.unstubAllGlobals());

describe('projectItems', () => {
  it('requests the id as one encoded segment, with no query', async () => {
    reply(200, '{"items":[],"suppressions":[]}');
    await projectItems('github.com/acme/re?po#x');
    expect(vi.mocked(fetch).mock.calls[0][0]).toBe('/api/v1/projects/github.com%2Facme%2Fre%3Fpo%23x/items');
  });

  it('reduces an error to the envelope message and hint', async () => {
    reply(500, '{"error":{"code":"internal","message":"Oops.","details":{"hint":"Try later."}}}');
    await expect(projectItems('a/b/c')).rejects.toMatchObject({ status: 500, message: 'Oops.', hint: 'Try later.' });
  });

  it('never echoes a body that is not an envelope', async () => {
    reply(502, '<pre>panic at /srv/x.go 10.0.0.7</pre>');
    const err = (await projectItems('a/b/c').catch((e) => e)) as ApiError;
    expect(err.message).not.toMatch(/panic|10\.0|srv/);
  });

  it('reads Retry-After on a 429, clamped', async () => {
    reply(429, '{"error":{"code":"rate_limited","message":"Slow down.","details":{"hint":"Wait."}}}', { 'Retry-After': '3' });
    await expect(projectItems('a/b/c')).rejects.toMatchObject({ status: 429, retryAfter: 3 });
    reply(429, '{}', { 'Retry-After': '999999' });
    await expect(projectItems('a/b/c')).rejects.toMatchObject({ retryAfter: 3600 });
  });
});

describe('projectOf', () => {
  it('takes the first project value and ignores anything else', () => {
    expect(projectOf({ project: ['a/b/c', 'x'] })).toBe('a/b/c');
    expect(projectOf({ project: 3 })).toBe('');
    expect(projectOf({})).toBe('');
  });
});
