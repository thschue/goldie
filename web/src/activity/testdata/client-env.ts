// A vitest environment for component tests that mount a page with Vue's own renderer: plain Node
// (no DOM library), but the .vue files are compiled for the client, not for server rendering.
import type { Environment } from 'vitest/runtime';

export default <Environment>{
  name: 'goldie-client',
  viteEnvironment: 'client',
  setup() {
    return { teardown() {} };
  },
};
