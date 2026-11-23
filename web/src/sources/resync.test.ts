import { describe, expect, it, vi } from 'vitest';
import { csrf } from '../auth/session';
import { resync } from './api';

describe('resync', () => {
  it('posts with the session CSRF token and throws the envelope on failure', async () => {
    csrf.value = 'tok';
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 202 }));
    vi.stubGlobal('fetch', fetchMock);
    await resync();
    const [path, init] = fetchMock.mock.calls[0];
    expect(path).toBe('/api/v1/sync');
    expect(init.method).toBe('POST');
    expect(init.headers['X-Goldie-CSRF']).toBe('tok');

    const body = { error: { message: 'Running.', details: { hint: 'Wait.' } } };
    fetchMock.mockResolvedValue(new Response(JSON.stringify(body), { status: 429 }));
    await expect(resync()).rejects.toMatchObject({ status: 429, hint: 'Wait.' });
    vi.unstubAllGlobals();
  });
});
