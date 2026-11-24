// Which answers mark the API unreachable (#638): a failed fetch and the proxy's 502
// `upstream_unavailable` envelope, nothing else.
import { beforeEach, describe, expect, it } from 'vitest';
import { note, unreachable } from './reachability';

const answer = (status: number, body: string) => new Response(body, { status });

beforeEach(() => (unreachable.value = false));

describe('note', () => {
  it('marks a failed fetch and the 502 upstream_unavailable envelope', async () => {
    await note();
    expect(unreachable.value).toBe(true);
    unreachable.value = false;
    await note(answer(502, '{"error":{"code":"upstream_unavailable","message":"x","details":{}}}'));
    expect(unreachable.value).toBe(true);
  });

  it('ignores another 502 and leaves the flag as it was', async () => {
    await note(answer(502, '<pre>panic</pre>'));
    await note(answer(502, '{"error":{"code":"internal"}}'));
    expect(unreachable.value).toBe(false);
  });

  it('clears on any other answer, errors included', async () => {
    unreachable.value = true;
    await note(answer(500, '{"error":{"code":"internal"}}'));
    expect(unreachable.value).toBe(false);
  });
});
