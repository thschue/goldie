// The article call (#498): a nested id travels as one %2F path segment
// (ADR 0016), an error shows only the envelope, and an absent `enforced` or
// `overrides` stays absent (#529: absent means unknown, never false).
import { afterEach, describe, expect, it, vi } from 'vitest';
import { getArticle } from './api';

const reply = (status: number, body: string) => vi.stubGlobal('fetch', vi.fn(async () => new Response(body, { status })));

afterEach(() => vi.unstubAllGlobals());

// The raw request target, before any URL normalisation.
const target = () => String(vi.mocked(fetch).mock.calls[0][0]);

const ok = { id: 'platform/deploy', name: 'Deploying to Kubernetes', keywords: ['kubernetes'], content: 'Use the platform chart.' };

describe('getArticle', () => {
  it('sends a nested id as one path segment with the slash percent-encoded', async () => {
    reply(200, JSON.stringify(ok));
    await getArticle('platform/deploy');
    const url = new URL(target(), 'file:///');
    expect(url.pathname).toBe('/api/v1/articles/platform%2Fdeploy');
    expect(url.search).toBe('');
  });

  it('sends a flat id as is, and encodes characters that would end the segment', async () => {
    reply(200, JSON.stringify({ ...ok, id: 'retry' }));
    await getArticle('retry');
    expect(new URL(target(), 'file:///').pathname).toBe('/api/v1/articles/retry');

    vi.unstubAllGlobals();
    reply(200, JSON.stringify(ok));
    await getArticle('a b/c?d#e%2Ff');
    const url = new URL(target(), 'file:///');
    expect(url.pathname).toBe('/api/v1/articles/a%20b%2Fc%3Fd%23e%252Ff');
    expect(url.search, 'a ? inside the id stayed in the path').toBe('');
    expect(url.hash).toBe('');
  });

  it('keeps an absent enforced and an absent overrides absent, never false or empty', async () => {
    reply(200, JSON.stringify(ok));
    const old = await getArticle('platform/deploy');
    expect('enforced' in old).toBe(false);
    expect('overrides' in old).toBe(false);

    vi.unstubAllGlobals();
    reply(200, JSON.stringify({ ...ok, enforced: false, overrides: [] }));
    const known = await getArticle('platform/deploy');
    expect(known.enforced).toBe(false);
    expect(known.overrides).toEqual([]);
  });

  it('returns status and replaced_by as the server sent them', async () => {
    reply(200, JSON.stringify({ ...ok, status: 'deprecated', replaced_by: 'article:platform/deploy-v2' }));
    const a = await getArticle('platform/deploy');
    expect(a.status).toBe('deprecated');
    expect(a.replaced_by).toBe('article:platform/deploy-v2');
  });

  it('rejects with the envelope message and hint of a 404', async () => {
    reply(404, '{"error":{"code":"not_found","message":"No article has this id.","details":{"hint":"Search for it."}}}');
    const err = (await getArticle('nope').catch((e) => e)) as Error & { hint?: string };
    expect(err).toBeInstanceOf(Error);
    expect(err.message).toBe('No article has this id.');
    expect(err.hint).toBe('Search for it.');
  });

  it('never echoes a body that is not an envelope', async () => {
    reply(502, '<pre>panic at /srv/x.go 10.0.0.7</pre>');
    const err = (await getArticle('x').catch((e) => e)) as Error & { hint?: string };
    expect(err).toBeInstanceOf(Error);
    expect(`${err.message} ${err.hint ?? ''}`).not.toMatch(/panic|srv|10\.0\.0\.7/);
  });

  it('rejects when the network fails, without the transport error text', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new TypeError('connect ECONNREFUSED 10.0.0.7:8080'))));
    const err = (await getArticle('x').catch((e) => e)) as Error & { hint?: string };
    expect(err).toBeInstanceOf(Error);
    expect(`${err.message} ${err.hint ?? ''}`).not.toMatch(/ECONNREFUSED|10\.0\.0\.7/);
  });

  it('rejects a 200 whose body is not an article', async () => {
    reply(200, '{"unexpected":true}');
    await expect(getArticle('x')).rejects.toBeInstanceOf(Error);
  });
});
