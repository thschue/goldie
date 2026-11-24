// Decisions calls (#736): GET /api/v1/decisions for a Project's view, filtered by lifecycle status.
import { get } from '../api/client';
import type { components } from '../api/schema';

export type Decision = components['schemas']['Decision'];
export type DecisionList = components['schemas']['DecisionList'];
export type DecisionStatus = components['schemas']['DecisionStatus'];
export type StatusFilter = DecisionStatus | 'all';

// The filter pills in handoff §3.8 order. `accepted` is the default: what agents get.
export const statusFilters: { value: StatusFilter; label: string }[] = [
  { value: 'accepted', label: 'accepted' },
  { value: 'proposed', label: 'proposed' },
  { value: 'superseded', label: 'superseded' },
  { value: 'deprecated', label: 'deprecated' },
  { value: 'rejected', label: 'rejected' },
  { value: 'all', label: 'All' },
];

// An empty Project is no `project` parameter: the API answers the global view (a blank value is a 400).
export function listDecisions(status: StatusFilter, project: string, signal?: AbortSignal) {
  const q = new URLSearchParams({ status });
  if (project) q.set('project', project);
  return get<DecisionList>(`/api/v1/decisions?${q}`, signal);
}

// One decision of any status, with its Markdown `content` and supersession `chain` (#737). The name is one
// path segment (ADR 0016); an empty Project is no `project` parameter.
export function getDecision(name: string, project: string, signal?: AbortSignal) {
  const q = project ? `?${new URLSearchParams({ project })}` : '';
  return get<Decision>(`/api/v1/decisions/${encodeURIComponent(name)}${q}`, signal);
}
