import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vitest/config';

// Unit tests (*.test.ts) run in Node; component tests (*.test.tsx) in jsdom.
export default defineConfig({
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  test: {
    globals: true,
    environment: 'node',
    environmentMatchGlobs: [['src/**/*.test.tsx', 'jsdom']],
    setupFiles: ['./src/test/setup.ts'],
  },
});
