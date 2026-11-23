// Agent assets calls (#587, #589, #647): the skills, agents and loops that answer in a Project's view, sorted by name.
import { get } from '../api/client';
import type { components } from '../api/schema';

export type Skill = components['schemas']['Skill'];
export type SkillList = components['schemas']['SkillList'];
export type Agent = components['schemas']['Agent'];
export type AgentList = components['schemas']['AgentList'];
export type Loop = components['schemas']['Loop'];
export type LoopList = components['schemas']['LoopList'];

// Sorted by name, ascending, whatever order the API answers in. Code-point order, as the server sorts.
const byName = (a: { name: string }, b: { name: string }) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0);

export const listSkills = (project: string, signal?: AbortSignal) =>
  get<SkillList>(`/api/v1/skills?project=${encodeURIComponent(project)}`, signal).then((l) => ({
    ...l,
    skills: [...l.skills].sort(byName),
  }));

export const listAgents = (project: string, signal?: AbortSignal) =>
  get<AgentList>(`/api/v1/agents?project=${encodeURIComponent(project)}`, signal).then((l) => ({
    ...l,
    agents: [...l.agents].sort(byName),
  }));

export const listLoops = (project: string, signal?: AbortSignal) =>
  get<LoopList>(`/api/v1/loops?project=${encodeURIComponent(project)}`, signal).then((l) => ({
    ...l,
    loops: [...l.loops].sort(byName),
  }));

// Source text is untrusted: shown as text, minus the invisible and bidirectional controls that
// could reorder or hide what a reader sees.
export const plain = (s: string | undefined) => (s ?? '').replace(/[​-‏‪-‮⁠-⁩﻿]/g, '');

// The prompt name MCP serves (internal/mcp/doc.go, #411).
export const promptName = (skill: string) => `goldie-${skill}`;

// Where `goldie pull` materialises an agent (docs/skills.md, GET /api/v1/bundle).
export const agentPath = (name: string) => `.claude/agents/goldie-${name}.md`;
