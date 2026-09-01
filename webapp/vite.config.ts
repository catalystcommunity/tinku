import { defineConfig } from 'vite'
import solid from 'vite-plugin-solid'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig({
  plugins: [solid()],
  resolve: {
    alias: {
      // `~` is the src root, matching tsconfig.app.json's path mapping and
      // the convention the other SPAs in this organization use, so nothing
      // deep in pages/ needs a ../../ import of the generated client.
      '~': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    // `npm run dev` proxies straight through to tinku-api's default port,
    // so the same relative paths the production build uses work unchanged
    // here. Only the two paths the api actually serves are proxied; the ops
    // port (9090) is deliberately not, because the browser has no business
    // reading /metrics.
    proxy: {
      '/csil': { target: 'http://localhost:8080', changeOrigin: false },
      '/auth': { target: 'http://localhost:8080', changeOrigin: false },
    },
  },
})
