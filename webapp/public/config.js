// Runtime configuration, replaced at container start.
//
// This file is what lets ONE built bundle serve every deployment: the api's
// address is a deployment fact, and a bundle that had it compiled in would
// need rebuilding per host. The image's nginx answers this path from an
// environment variable instead (see webapp/nginx.conf); the copy here is
// what a `vite dev` run serves, and it deliberately configures nothing so
// that the default applies — the same host, on the api's port.
//
// To pin it by hand, set:
//   window.__TINKU__ = { apiBaseUrl: "https://api.example.com" };
// An empty string means "same origin", for a deployment with something in
// front proxying /csil and /auth.
window.__TINKU__ = window.__TINKU__ || {};
