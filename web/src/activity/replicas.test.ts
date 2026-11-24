import { describe, expect, it } from 'vitest';
import { agoText } from './rows';
import { split } from './stream';

describe('replica_status and ago', () => {
  it('reads a replica_status frame and ignores a malformed one', () => {
    const out = split(
      'event: replica_status\ndata: {"replica":"goldie-1","up":false,"last_seen":"2026-01-01T00:00:00Z"}\n\n' +
        'event: replica_status\ndata: {"replica":"","up":false}\n\nevent: replica_status\ndata: {"replica":"x","up":"no"}\n\n',
    );
    expect(out.frames).toEqual([{ type: 'status', replica: 'goldie-1', up: false, lastSeen: '2026-01-01T00:00:00Z' }]);
  });

  it('words how long ago, and never a wrong number for an unreadable time', () => {
    const now = Date.parse('2026-01-01T01:00:00Z');
    expect(agoText('2026-01-01T00:59:30Z', now)).toBe('30 seconds ago');
    expect(agoText('2026-01-01T00:58:00Z', now)).toBe('2 minutes ago');
    expect(agoText('2026-01-01T00:00:00Z', now)).toBe('1 hour ago');
    expect(agoText('nope', now)).toBe('a moment ago');
  });
});
