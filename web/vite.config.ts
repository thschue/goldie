import vue from '@vitejs/plugin-vue';
import { defineConfig } from 'vitest/config';

// ./run ui builds into internal/ui/dist/app, which the Go binary embeds
// (ADR 0019). Assets are never inlined: the CSP allows fonts only from 'self',
// so a font as a data: URL would be blocked.
export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: '../internal/ui/dist/app',
    emptyOutDir: true,
    assetsInlineLimit: 0,
  },
  // Component tests render with Vue's server renderer: no DOM library.
  // ActivityView's test mounts the page with Vue's own renderer, so its .vue files compile for the
  // client; every other test stays on plain node.
  test: {
    projects: [
      {
        extends: true,
        test: { name: 'node', include: ['src/**/*.test.ts'], exclude: ['src/activity/ActivityView.test.ts'], environment: 'node' },
      },
      {
        extends: true,
        test: { name: 'client', include: ['src/activity/ActivityView.test.ts'], environment: './src/activity/testdata/client-env.ts' },
      },
    ],
  },
});
