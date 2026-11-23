// The Agent assets API helpers (#587): the Project rides as one encoded query value, the
// cards sort by name, and Source text loses its invisible and bidirectional controls.
import { afterEach, expect, it, vi } from 'vitest';
import { agentPath, listAgents, listSkills, plain, promptName } from './api';

afterEach(() => vi.unstubAllGlobals());

it('requests the Project as an encoded query and sorts by name', async () => {
  const body = { skills: ['b', 'c', 'a'].map((name) => ({ name, files: [] })), hint: 'h' };
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body))));
  const list = await listSkills('github.com/acme/re&po');
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe('/api/v1/skills?project=github.com%2Facme%2Fre%26po');
  expect(list.skills.map((s) => s.name)).toEqual(['a', 'b', 'c']);
  expect(list.hint).toBe('h');
});

it('requests the agents with the Project as an encoded query and sorts by name', async () => {
  const body = { agents: ['b', 'c', 'a'].map((name) => ({ name, description: '' })), hint: 'h' };
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body))));
  const list = await listAgents('github.com/acme/re&po');
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe('/api/v1/agents?project=github.com%2Facme%2Fre%26po');
  expect(list.agents.map((a) => a.name)).toEqual(['a', 'b', 'c']);
  expect(list.hint).toBe('h');
});

it('agentPath is the materialised .claude/agents/goldie-<name>.md', () => {
  expect(agentPath('reviewer')).toBe('.claude/agents/goldie-reviewer.md');
});

it('plain drops bidi and zero-width controls and promptName is goldie-<name>', () => {
  expect(plain('safe‮txt.exe​')).toBe('safetxt.exe');
  expect(plain(undefined)).toBe('');
  expect(promptName('deploy')).toBe('goldie-deploy');
});
