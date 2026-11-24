// useCopy (#590): the result message shows after a copy, clears on demand and on its own, and
// reports a refused clipboard. The text written is exactly the text given.
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createSSRApp, defineComponent, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { useCopy } from './useCopy';

async function setup() {
  let api!: ReturnType<typeof useCopy>;
  const app = createSSRApp(
    defineComponent({
      setup() {
        api = useCopy('digest');
        return () => h('div');
      },
    }),
  );
  await renderToString(app);
  return api;
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it('writes the given text and says Copied for 2.5 s', async () => {
  const writeText = vi.fn(async () => {});
  vi.stubGlobal('navigator', { clipboard: { writeText } });
  const { status, copy } = await setup();
  await copy('abc');
  expect(writeText).toHaveBeenCalledWith('abc');
  expect(status.value).toBe('Copied');
  vi.advanceTimersByTime(2500);
  expect(status.value).toBe('');
});

it('clear drops the message at once', async () => {
  vi.stubGlobal('navigator', { clipboard: { writeText: async () => {} } });
  const { status, copy, clear } = await setup();
  await copy('abc');
  clear();
  expect(status.value).toBe('');
});

it('names what to select when the clipboard refuses', async () => {
  vi.stubGlobal('navigator', {
    clipboard: {
      writeText: async () => {
        throw new Error('denied');
      },
    },
  });
  const { status, copy } = await setup();
  await copy('abc');
  expect(status.value).toBe('Copy failed. Select the digest and copy it.');
});
