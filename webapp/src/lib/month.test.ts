import { describe, expect, it } from "vitest";
import { addMonths, buildCells, groupByDay, monthWindow, startOfWeek, ymd, ymdInZone } from "./month";

describe("the month grid", () => {
  it("always has six Monday-first weeks", () => {
    // February 2026 starts on a Sunday, which is the case that would give a
    // five-row grid if the row count followed the month.
    const cells = buildCells(2026, 1, "2026-02-15");
    expect(cells).toHaveLength(42);
    expect(startOfWeek(new Date(2026, 1, 1)).getDay()).toBe(1);
    expect(cells[0].inMonth).toBe(false); // a January day, leading in
    expect(cells.filter((c) => c.inMonth)).toHaveLength(28);
  });

  it("marks today, the weekend and the neighbouring months", () => {
    const cells = buildCells(2026, 8, "2026-09-10");
    const today = cells.find((c) => c.isToday);
    expect(today?.ymd).toBe("2026-09-10");
    expect(cells.filter((c) => c.isWeekend)).toHaveLength(12); // six weekends
  });

  it("steps between months without rolling the day", () => {
    expect(addMonths(2026, 11, 1)).toEqual({ year: 2027, month: 0 });
    expect(addMonths(2026, 0, -1)).toEqual({ year: 2025, month: 11 });
  });
});

describe("placing an event", () => {
  // The rule the whole calendar rests on: an event is on the day it happens
  // WHERE IT IS. 03:00 UTC on the 11th is still Thursday the 10th in
  // Denver, and Denver is where the climb is.
  it("uses the event's own timezone, not the reader's", () => {
    const instant = "2026-09-11T01:00:00Z";
    expect(ymdInZone(instant, "America/Denver")).toBe("2026-09-10");
    expect(ymdInZone(instant, "Asia/Tokyo")).toBe("2026-09-11");
  });

  it("gets the offset right across a daylight-saving change", () => {
    // 01:00 UTC is the evening before in Denver in September (UTC-6) and in
    // November (UTC-7) alike — the zone, not a fixed offset, is what says so.
    expect(ymdInZone("2026-09-11T01:00:00Z", "America/Denver")).toBe("2026-09-10");
    expect(ymdInZone("2026-11-13T01:00:00Z", "America/Denver")).toBe("2026-11-12");
  });

  it("falls back to UTC for a zone the browser does not know", () => {
    expect(ymdInZone("2026-09-11T01:00:00Z", "Mars/Olympus_Mons")).toBe("2026-09-11");
  });

  it("groups by day and keeps the order it was given", () => {
    const events = [
      { startsAt: "2026-09-10T19:00:00Z", timezone: "UTC", id: "first" },
      { startsAt: "2026-09-10T21:00:00Z", timezone: "UTC", id: "second" },
      { startsAt: "2026-09-11T19:00:00Z", timezone: "UTC", id: "third" },
    ];
    const byDay = groupByDay(events);
    expect([...byDay.keys()]).toEqual(["2026-09-10", "2026-09-11"]);
    expect(byDay.get("2026-09-10")?.map((e) => e.id)).toEqual(["first", "second"]);
  });
});

describe("the window a month asks the server for", () => {
  it("covers every cell the grid will draw, and a day either side", () => {
    const { from, to } = monthWindow(2026, 8);
    const cells = buildCells(2026, 8, "2026-09-01");
    // Every visible cell is inside the window, so no drawn day can be
    // missing events the server has.
    expect(ymd(from) < cells[0].ymd).toBe(true);
    expect(ymd(to) > cells[41].ymd).toBe(true);
  });
});
