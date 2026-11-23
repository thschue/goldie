// The Loops API helper (#647): the Project rides as one encoded query value and the loops sort by name.
import { afterEach, expect, it, vi } from 'vitest';
import { listLoops } from './api';

afterEach(() => vi.unstubAllGlobals());

it('requests loops with the Project as an encoded query and sorts them by name', async () => {
  const body = { loops: ['b', 'a'].map((name) => ({ name, closure: { agents: [], skills: [] } })) };
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body))));
  const list = await listLoops('github.com/acme/re&po');
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe('/api/v1/loops?project=github.com%2Facme%2Fre%26po');
  expect(list.loops.map((l) => l.name)).toEqual(['a', 'b']);
});
