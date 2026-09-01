// The i18n seam's own rules, tested at the seam rather than through a
// component: these are the properties that make a catalog translatable, and
// each one is a thing that quietly breaks when somebody takes a shortcut.
import { describe, expect, it } from "vitest";
import { negotiate } from "./index";
import { enUS, type MessageKey } from "./en-US";
import { describeRecurrence } from "./recurrence";

const t = ((key: MessageKey, values?: Record<string, string | number>) => {
  const template = (enUS as Record<string, string>)[key] ?? key;
  return template.replace(/\{(\w+)\}/g, (whole, name: string) =>
    values && name in values ? String(values[name]) : whole,
  );
}) as Parameters<typeof describeRecurrence>[0];

describe("locale negotiation", () => {
  it("prefers an exact tag", () => {
    expect(negotiate(["en-US"], ["en-US", "es-MX"])).toBe("en-US");
  });

  it("falls back to the language when the region is not carried", () => {
    // Somebody asking for en-GB gets English, not the default locale.
    expect(negotiate(["en-GB"], ["en-US", "es-MX"])).toBe("en-US");
  });

  it("takes the first preference it can serve", () => {
    expect(negotiate(["fr-FR", "es-MX", "en-US"], ["en-US", "es-MX"])).toBe("es-MX");
  });

  it("falls back to the default when it can serve nothing", () => {
    expect(negotiate(["ja-JP"], ["en-US"])).toBe("en-US");
  });
});

describe("the catalog", () => {
  it("gives every plural message both English categories", () => {
    // A catalog with `_one` and no `_other` renders the key itself for
    // every count but 1, which is the kind of bug nobody sees until a list
    // has two things in it.
    const bases = new Set<string>();
    for (const key of Object.keys(enUS)) {
      const match = key.match(/^(.*)_(one|other)$/);
      if (match) bases.add(match[1]!);
    }
    expect(bases.size).toBeGreaterThan(0);
    for (const base of bases) {
      expect(enUS).toHaveProperty(`${base}_one`);
      expect(enUS).toHaveProperty(`${base}_other`);
    }
  });

  it("puts values in placeholders rather than leaving them to be concatenated", () => {
    // Every message that names a count has to interpolate it. One that does
    // not is a message somebody is building by gluing strings together, and
    // a translator cannot reorder what a component concatenated.
    for (const [key, template] of Object.entries(enUS)) {
      if (/_(one|other)$/.test(key)) {
        expect(template, `${key} does not interpolate {count}`).toContain("{count}");
      }
    }
  });
});

describe("describing a recurrence rule", () => {
  it("describes the second Thursday of every month", () => {
    expect(
      describeRecurrence(t, { freq: "monthly", interval: 1, weekday: "thursday", ordinal: 2 }),
    ).toBe("The second Thursday of every month");
  });

  it("describes the first Saturday of the quarter", () => {
    expect(
      describeRecurrence(t, { freq: "quarterly", interval: 1, weekday: "saturday", ordinal: 1 }),
    ).toBe("The first Saturday of every quarter");
  });

  it("distinguishes every other Thursday from every second Thursday", () => {
    const everyOther = describeRecurrence(t, {
      freq: "weekly",
      interval: 2,
      weekday: "thursday",
    });
    const everySecond = describeRecurrence(t, {
      freq: "monthly",
      interval: 1,
      weekday: "thursday",
      ordinal: 2,
    });
    expect(everyOther).toBe("Every 2 weeks on Thursday");
    expect(everyOther).not.toBe(everySecond);
  });

  it("describes a day-of-month rule without an ordinal or a weekday", () => {
    expect(describeRecurrence(t, { freq: "monthly", interval: 1, dayOfMonth: 15 })).toBe(
      "The 15 of every month",
    );
  });

  it("appends the local clock time when there is one", () => {
    expect(
      describeRecurrence(
        t,
        { freq: "monthly", interval: 1, weekday: "thursday", ordinal: 2 },
        "19:00",
      ),
    ).toBe("The second Thursday of every month, at 19:00");
  });
});
