import { afterEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_API_PORT, apiBaseUrl } from "./apiBase";

// The client and the api are two origins. Getting this rule wrong does not
// fail loudly: the page loads, and every call goes somewhere that is not the
// api — which reads as "the server is down" or "I am logged out".

function serveFrom(href: string): void {
  const url = new URL(href);
  vi.stubGlobal("window", {
    location: {
      protocol: url.protocol,
      hostname: url.hostname,
      port: url.port,
      href,
    },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("where the api is", () => {
  it("defaults to the same host on the api's port", () => {
    // The case this exists for: a port-forward of both ports. The page came
    // from somehost.tld:8080, so the api is somehost.tld:5080 — no
    // configuration anywhere.
    serveFrom("http://somehost.tld:8080/gatherings");
    expect(apiBaseUrl()).toBe(`http://somehost.tld:${DEFAULT_API_PORT}`);
  });

  it("keeps the scheme it was loaded over", () => {
    // An https page calling an http api is blocked as mixed content, so the
    // protocol has to follow the page rather than being assumed.
    serveFrom("https://tinku.example/gatherings");
    expect(apiBaseUrl()).toBe(`https://tinku.example:${DEFAULT_API_PORT}`);
  });

  it("stays relative when the page is already on the api's port", () => {
    // Something is serving both, so pointing a URL at the origin the page
    // already has would add a cross-origin request for no reason.
    serveFrom(`http://localhost:${DEFAULT_API_PORT}/`);
    expect(apiBaseUrl()).toBe("");
  });

  it("obeys an injected base URL", () => {
    serveFrom("http://somehost.tld:8080/");
    window.__TINKU__ = { apiBaseUrl: "https://api.example.com/" };
    // The trailing slash is dropped: the path is appended to this, and
    // "https://api.example.com//csil/v1/rpc" is a 404 waiting to happen.
    expect(apiBaseUrl()).toBe("https://api.example.com");
  });

  it("ignores an injected empty value and falls back to the default", () => {
    // envsubst renders an unset variable as empty, so an empty value cannot
    // be read as an instruction: it is indistinguishable from nobody having
    // configured anything.
    serveFrom("http://somehost.tld:8080/");
    window.__TINKU__ = { apiBaseUrl: "" };
    expect(apiBaseUrl()).toBe(`http://somehost.tld:${DEFAULT_API_PORT}`);
  });

  it("takes '/' as the way to ask for same origin", () => {
    // The deployment with something in front proxying /csil. It has to be
    // sayable, and it has to be distinguishable from an unset variable.
    serveFrom("http://somehost.tld:8080/");
    window.__TINKU__ = { apiBaseUrl: "/" };
    expect(apiBaseUrl()).toBe("");
  });

  it("is relative when there is no window at all", () => {
    // A unit test or a server-side render. Relative is the only answer that
    // cannot be wrong, because there is no page origin to derive from.
    vi.stubGlobal("window", undefined);
    expect(apiBaseUrl()).toBe("");
  });
});
