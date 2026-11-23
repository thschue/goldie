import { describe, expect, it } from 'vitest';
import { failingFirst, when } from './api';

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
