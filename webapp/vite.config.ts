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
    // 8080 is the UI, here and in the compose stack, so the address a person
    // types is the same whichever way they started it.
    port: 8080,
    // Bind every interface, and accept any Host header.
    //
    // Vite binds IPv6 loopback and refuses a Host it does not recognise, so
    // out of the box a port-forward to somehost.tld:8080 finds nothing
    // listening and then gets "Blocked request. This host is not allowed".
    // Both of those are right for a dev server on a laptop and wrong for
    // the way this one is actually reached.
    //
    // Know what this opens: `./tools.sh dev-web` runs against an api with
    // development sign-in on, where anybody who can reach it can sign in as
    // anybody — devadmin included. Run it on a network you trust, or put
    // the port-forward in front of it rather than the interface.
    host: true,
    allowedHosts: true,
    // Fail rather than wander to 8081: a dev server that silently moves is a
    // dev server whose address in the README is wrong.
    strictPort: true,
    // `npm run dev` proxies straight through to tinku-api's default port,
    // so the same relative paths the production build uses work unchanged
    // here. Only the two paths the api actually serves are proxied; the ops
    // port (9090) is deliberately not, because the browser has no business
    // reading /metrics.
    proxy: {
      '/csil': { target: 'http://localhost:5080', changeOrigin: false },
      '/auth': { target: 'http://localhost:5080', changeOrigin: false },
    },
  },
})
