import { describe, expect, it } from 'vitest';
import { addText, canSearch, missBoard } from './rows';
import type { ActivityEvent } from './stream';

const ev = (id: number, o: Partial<ActivityEvent> = {}): ActivityEvent => ({
  id,
  time: '2026-11-24T10:00:00Z',
  kind: 'search',
  query: 'q',
  project: '',
  token: 'ci',
  client_version: '',
  trace_id: '',
  miss: true,
  replica: 'goldie-0',
  late: false,
  ...o,
});

describe('missBoard', () => {
  it('groups misses by query, most frequent first, at most four, latest event first', () => {
    const feed = [
      ev(9, { query: 'a' }),
      ev(8, { query: 'b' }),
      ev(7, { query: 'b', project: 'p1' }),
      ev(6, { query: 'c', miss: false }),
      ev(5, { query: 'd' }),
      ev(4, { query: 'e' }),
      ev(3, { query: 'f' }),
      ev(2, { query: 'b', project: 'p2' }),
    ];
    const board = missBoard(feed);
    expect(board.map((g) => g.query)).toEqual(['b', 'a', 'd', 'e']);
    expect(board[0]).toMatchObject({ count: 3, projects: 2 });
    expect(board[0].latest.id).toBe(8);
  });
});

describe('addText and canSearch', () => {
  it('words what to add per kind', () => {
    expect(addText(ev(1, { kind: 'get', query: 'skill:x' }))).toBe('Add this skill to the Source repo ↗');
    expect(addText(ev(1, { kind: 'get', query: 'agent:x' }))).toBe('Add this agent to the Source repo ↗');
    expect(addText(ev(1, { kind: 'get', query: 'x' }))).toBe('Add this skill or agent to the Source repo ↗');
    expect(addText(ev(1, { kind: 'lookup', query: 'a/b' }))).toBe('Start this article in the Source repo ↗');
  });
  it('offers the search for a search and a lookup only', () => {
    expect(canSearch(ev(1))).toBe(true);
    expect(canSearch(ev(1, { kind: 'lookup' }))).toBe(true);
    expect(canSearch(ev(1, { kind: 'get' }))).toBe(false);
  });
});
