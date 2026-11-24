import { describe, expect, it } from 'vitest';
import { failingFirst, origin, when } from './api';

describe('failingFirst', () => {
  it('puts failing Sources first and keeps the API order otherwise', () => {
    const a = { revision: 'a' };
    const b = { revision: 'b', last_error: 'x' };
    const c = { revision: 'c' };
    const d = { revision: 'd', last_error: 'y' };
    expect(failingFirst([a, b, c, d])).toEqual([b, d, a, c]);
  });
});

describe('when', () => {
  it('formats a time in UTC and shows anything unparseable as text', () => {
    expect(when('2026-11-23T10:00:00Z')).toContain('UTC');
    expect(when('not a time')).toBe('not a time');
  });
});

describe('origin', () => {
  it('reads a Secret Source from its name and calls any other Source env', () => {
    expect(origin('secret:ops/extra-docs')).toBe('Secret ops/extra-docs');
    expect(origin('team')).toBe('env GOLDIE_SOURCES');
    expect(origin()).toBe('env GOLDIE_SOURCES');
  });
});
