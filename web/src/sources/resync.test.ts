import { describe, expect, it, vi } from 'vitest';
import { csrf } from '../auth/session';
import { resync, waitForSync } from './api';

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

describe('waitForSync', () => {
  const answer = (last_poll: string) => new Response(JSON.stringify({ sources: [{ last_poll }], warnings: [] }));

  it('resolves once a last_poll differs and stops at the limit otherwise', async () => {
    const fetchMock = vi.fn().mockImplementationOnce(async () => answer('a')).mockImplementation(async () => answer('b'));
    vi.stubGlobal('fetch', fetchMock);
    const done = await waitForSync([{ last_poll: 'a' }], new AbortController().signal, 1, 1000);
    expect(done).toEqual([{ last_poll: 'b' }]);
    expect(fetchMock).toHaveBeenCalledTimes(2);

    fetchMock.mockImplementation(async () => answer('a'));
    const late = await waitForSync([{ last_poll: 'a' }], new AbortController().signal, 1, 20);
    expect(late).toEqual([{ last_poll: 'a' }]);
    vi.unstubAllGlobals();
  });

  it('rejects when aborted', async () => {
    const c = new AbortController();
    const p = waitForSync([], c.signal, 1000, 5000);
    c.abort();
    await expect(p).rejects.toMatchObject({ name: 'AbortError' });
  });
});
