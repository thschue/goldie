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
  test: { include: ['src/**/*.test.ts'], environment: 'node' },
});
