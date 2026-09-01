import { defineConfig } from 'vitest/config'
import solid from 'vite-plugin-solid'
import { fileURLToPath, URL } from 'node:url'

// Standalone rather than merged with vite.config.ts: merging applies the
// solid() plugin twice, which pulls in two copies of solid-js and produces
// "You appear to have multiple instances of Solid" plus spurious
// "computations created outside a createRoot" warnings on every render.
export default defineConfig({
  plugins: [solid()],
  resolve: {
    alias: { '~': fileURLToPath(new URL('./src', import.meta.url)) },
    // vite-plugin-solid needs solid-js's browser/development build under
    // the test runner too; without these conditions the components resolve
    // to the server renderer and never mount.
    conditions: ['development', 'browser'],
  },
  test: {
    environment: 'happy-dom',
    setupFiles: ['./vitest.setup.ts'],
  },
})
