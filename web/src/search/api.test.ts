// The search call (#497): the query is data in one parameter, the filter is the
// client's, an error shows only the envelope, and an absent `enforced` or
// `overrides` stays absent (#529: absent means unknown, never false).
import { afterEach, describe, expect, it, vi } from 'vitest';
import { searchItems } from './api';

const reply = (status: number, body: string, headers: Record<string, string> = {}) =>
  vi.stubGlobal('fetch', vi.fn(async () => new Response(body, { status, headers })));

afterEach(() => vi.unstubAllGlobals());

const requested = () => new URL(String(vi.mocked(fetch).mock.calls[0][0]), 'file:///');

describe('searchItems', () => {
  it('sends the query as one encoded q parameter, to /api/v1/search only', async () => {
    reply(200, '{"query":"x","results":[]}');
    await searchItems('database & logs #1 ?kind=skill');
    const url = requested();
    expect(url.pathname).toBe('/api/v1/search');
    expect([...url.searchParams.keys()]).toContain('q');
    expect(url.searchParams.get('q')).toBe('database & logs #1 ?kind=skill');
    expect(url.searchParams.getAll('kind'), 'the Kind filter is applied on the client').toEqual([]);
    expect(url.hash).toBe('');
  });

  it('keeps an absent enforced and an absent overrides absent, never false or empty', async () => {
    reply(
      200,
      JSON.stringify({
        query: 'x',
        results: [
          { id: 'a/old', name: 'Old', kind: 'article', scope: 'global', keywords: [], score: 1, matched: ['x'] },
          { id: 'a/new', name: 'New', kind: 'article', scope: 'global', keywords: [], score: 1, matched: ['x'], enforced: false, overrides: [] },
        ],
      }),
    );
    const { results } = await searchItems('x');
    expect('enforced' in results[0]).toBe(false);
    expect('overrides' in results[0]).toBe(false);
    expect(results[1].enforced).toBe(false);
    expect(results[1].overrides).toEqual([]);
  });

  it('rejects with the envelope message and hint, and never echoes a body that is not an envelope', async () => {
    reply(400, '{"error":{"code":"invalid_request","message":"The search words are missing.","details":{"hint":"Pass q."}}}');
    const err = (await searchItems('').catch((e) => e)) as Error;
    expect(err).toBeInstanceOf(Error);
    expect(err.message).toContain('The search words are missing.');

    reply(502, '<pre>panic at /srv/x.go 10.0.0.7</pre>');
    const other = (await searchItems('x').catch((e) => e)) as Error;
    expect(other).toBeInstanceOf(Error);
    expect(other.message).not.toMatch(/panic|srv|10\.0\.0\.7/);
  });

  it('rejects when the network fails, without the transport error text', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new TypeError('connect ECONNREFUSED 10.0.0.7:8080'))));
    const err = (await searchItems('x').catch((e) => e)) as Error;
    expect(err).toBeInstanceOf(Error);
    expect(err.message).not.toMatch(/ECONNREFUSED|10\.0\.0\.7/);
  });
});
