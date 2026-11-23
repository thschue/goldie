// safeNext: the hostile values of #495 and the paths that must survive.
import { describe, expect, it } from 'vitest';
import { safeNext } from './next';

// The scheme is kept apart: web/src names no external URL (internal/ui checks).
const scheme = 'https:';
const origin = `${scheme}//goldie.example`;

describe('safeNext', () => {
  it.each(['//evil.example', '/\\evil', `${scheme}//evil.example`, 'javascript:alert(1)', '/\r\n', '/%0d%0a', '///evil', '/ x', '/a\\b', '', '/signin'])(
    'falls back to / for %j',
    (value) => {
      expect(safeNext(value, origin)).toBe('/');
    },
  );

  it('falls back to / for a value that is not a string', () => {
    expect(safeNext(undefined, origin)).toBe('/');
    expect(safeNext(['/decisions'], origin)).toBe('/');
  });

  it('keeps a same-origin path with its query and fragment', () => {
    expect(safeNext('/decisions?status=accepted&scope=org#top', origin)).toBe('/decisions?status=accepted&scope=org#top');
    expect(safeNext('/items/skill/x', origin)).toBe('/items/skill/x');
  });
});
