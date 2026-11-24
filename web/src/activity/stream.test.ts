import { describe, expect, it } from 'vitest';
import { add, maxRows, msText, resultText, surfaceOf } from './rows';
import { split, type ActivityEvent } from './stream';

const ev = (id: number, o: Partial<ActivityEvent> = {}): ActivityEvent => ({
  id,
  time: '2026-11-23T10:00:00Z',
  kind: 'search',
  query: `q${id}`,
  project: '',
  token: 'ci',
  client_version: '',
  trace_id: '',
  miss: false,
  replica: 'goldie-0',
  late: false,
  ...o,
});

describe('split', () => {
  it('cuts whole frames and keeps the unfinished rest', () => {
    const out = split(`data: ${JSON.stringify(ev(1))}\n\nevent: live\ndata: {}\n\ndata: {"id`);
    expect(out.frames.map((f) => f.type)).toEqual(['event', 'live']);
    expect(out.rest).toBe('data: {"id');
  });

  it('reads CRLF, a dropped frame, and ignores comments, unknown types and bad data', () => {
    const out = split(': keep-alive\r\n\r\nevent: dropped\r\ndata: {"dropped":3}\r\n\r\nevent: other\r\ndata: {}\r\n\r\ndata: nope\r\n\r\n');
    expect(out.frames).toEqual([{ type: 'dropped', count: 3 }]);
  });

  it('drops an event that is not an ActivityEvent', () => {
    expect(split('data: {"id":"1"}\n\n').frames).toEqual([]);
  });
});

describe('rows', () => {
  it('puts the newest first and keeps the newest maxRows', () => {
    const rows = add([], Array.from({ length: maxRows + 5 }, (_, i) => ev(i + 1)));
    expect(rows).toHaveLength(maxRows);
    expect(rows[0].id).toBe(maxRows + 5);
    expect(add(rows, [ev(9999)])[0].id).toBe(9999);
  });

  it('words a miss by what was asked for', () => {
    expect(resultText(ev(1))).toBe('Served');
    expect(resultText(ev(1, { miss: true, kind: 'get', query: 'skill:x' }))).toMatch(/skill/);
    expect(resultText(ev(1, { miss: true, kind: 'get', query: 'agent:x' }))).toMatch(/agent/);
    expect(resultText(ev(1, { miss: true, kind: 'lookup', query: 'a/b' }))).toMatch(/article/);
    expect(resultText(ev(1, { miss: true }))).not.toMatch(/skill|agent|article/);
  });
});

// #810: rows.ts gives surfaceOf(event), the pill an event counts under: mcp, cli or ui as sent, and
// other for any other value, and msText(microseconds), a latency in milliseconds with one decimal.
describe('surfaceOf', () => {
  it('is the surface the event came from, and other for a value it does not know', () => {
    expect(surfaceOf(ev(1, { via: 'mcp' }))).toBe('mcp');
    expect(surfaceOf(ev(1, { via: 'cli' }))).toBe('cli');
    expect(surfaceOf(ev(1, { via: 'ui' }))).toBe('ui');
    expect(surfaceOf(ev(1, { via: 'other' }))).toBe('other');
    expect(surfaceOf(ev(1, { via: 'robot' }))).toBe('other');
  });
});

describe('msText', () => {
  it('shows microseconds as milliseconds with one decimal', () => {
    expect(msText(850)).toMatch(/^0\.9\s?ms$/);
    expect(msText(12_345)).toMatch(/^12\.3\s?ms$/);
    expect(msText(51_000)).toMatch(/^51\.0\s?ms$/);
  });
});
