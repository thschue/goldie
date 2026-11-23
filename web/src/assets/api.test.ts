// The Agent assets API helpers (#587): the Project rides as one encoded query value, the
// cards sort by name, and Source text loses its invisible and bidirectional controls.
import { afterEach, expect, it, vi } from 'vitest';
import { listLoops, listSkills, plain, promptName } from './api';

afterEach(() => vi.unstubAllGlobals());

it('requests the Project as an encoded query and sorts by name', async () => {
  const body = { skills: ['b', 'c', 'a'].map((name) => ({ name, files: [] })), hint: 'h' };
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body))));
  const list = await listSkills('github.com/acme/re&po');
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe('/api/v1/skills?project=github.com%2Facme%2Fre%26po');
  expect(list.skills.map((s) => s.name)).toEqual(['a', 'b', 'c']);
  expect(list.hint).toBe('h');
});

it('plain drops bidi and zero-width controls and promptName is goldie-<name>', () => {
  expect(plain('safe‮txt.exe​')).toBe('safetxt.exe');
  expect(plain(undefined)).toBe('');
  expect(promptName('deploy')).toBe('goldie-deploy');
});

it('requests loops with the Project as an encoded query and sorts them by name', async () => {
  const body = { loops: ['b', 'a'].map((name) => ({ name, closure: { agents: [], skills: [] } })) };
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body))));
  const list = await listLoops('github.com/acme/re&po');
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe('/api/v1/loops?project=github.com%2Facme%2Fre%26po');
  expect(list.loops.map((l) => l.name)).toEqual(['a', 'b']);
});
