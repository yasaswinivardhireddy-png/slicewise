/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// In development, Vite proxies the API (including the event stream) to a local `slicewise` server.
export default defineConfig({
  plugins: [react()],
  server: { proxy: { '/api': process.env.API_URL ?? 'http://127.0.0.1:8080' } },
  // No source maps (the server would publish them) and no inline module-preload polyfill, which the
  // server's Content-Security-Policy (script-src 'self') would block.
  build: { sourcemap: false, target: 'es2022', modulePreload: { polyfill: false } },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
