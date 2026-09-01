import { describe, expect, it } from "vitest";
import { instantFromWallClock, wallClockFromInstant } from "./zonedTime";

// These are the cases the naive `new Date(wallClock)` gets wrong. Each one
// is a time somebody could plausibly schedule.
describe("reading a wall clock in a named zone", () => {
  it("resolves a winter time in a zone behind UTC", () => {
    // 19:00 on 12 February in Denver is MST, UTC-7.
    expect(instantFromWallClock("2026-02-12T19:00", "America/Denver").toISOString()).toBe(
      "2026-02-13T02:00:00.000Z",
    );
  });

  it("resolves a summer time in the same zone across the offset change", () => {
    // 19:00 on 10 September in Denver is MDT, UTC-6 — a different offset for
    // the same wall-clock reading and the same zone.
    expect(instantFromWallClock("2026-09-10T19:00", "America/Denver").toISOString()).toBe(
      "2026-09-11T01:00:00.000Z",
    );
  });

  it("resolves a zone ahead of UTC by a half hour", () => {
    // A whole-hour offset can hide an arithmetic slip; +05:30 cannot.
    expect(instantFromWallClock("2026-03-12T19:30", "Asia/Kolkata").toISOString()).toBe(
      "2026-03-12T14:00:00.000Z",
    );
  });

  it("resolves UTC itself", () => {
    expect(instantFromWallClock("2026-03-12T14:00", "UTC").toISOString()).toBe(
      "2026-03-12T14:00:00.000Z",
    );
  });

  it("does not depend on the browser's own zone", () => {
    // The whole point: the same input and zone give the same instant no
    // matter where the person filling the form is sitting.
    const denver = instantFromWallClock("2026-02-12T19:00", "America/Denver");
    const kolkata = instantFromWallClock("2026-02-12T19:00", "Asia/Kolkata");
    expect(denver.toISOString()).not.toBe(kolkata.toISOString());
  });

  it("refuses input that is not a wall-clock reading", () => {
    // An empty datetime-local field would otherwise become an Invalid Date
    // and be sent as one.
    expect(() => instantFromWallClock("", "UTC")).toThrow(RangeError);
    expect(() => instantFromWallClock("tomorrow", "UTC")).toThrow(RangeError);
  });
});

describe("round-tripping", () => {
  it("returns the reading it started from", () => {
    for (const [wall, zone] of [
      ["2026-02-12T19:00", "America/Denver"],
      ["2026-09-10T19:00", "America/Denver"],
      ["2026-03-12T19:30", "Asia/Kolkata"],
      ["2026-06-01T00:00", "Pacific/Auckland"],
    ] as const) {
      const instant = instantFromWallClock(wall, zone);
      expect(wallClockFromInstant(instant, zone)).toBe(wall);
    }
  });
});
