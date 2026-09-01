// Turning a wall-clock time somebody typed into the UTC instant it names.
//
// The trap this exists to close: `new Date("2026-02-12T19:00")` resolves in
// the BROWSER's zone. A form that also lets the organizer pick a timezone
// then produces an instant that disagrees with the label next to it — set
// 19:00 while sitting in Denver, change the zone to New York, and the event
// is stored two hours off with nothing on screen to say so.
//
// Every instant tinku stores is UTC (see docs/OPERATING.md). A timezone is
// for display and for entry. This is the entry half: the one place a local
// wall-clock reading plus a zone becomes an instant.

/**
 * The UTC instant at which `wallClock` reads on the clock in `timeZone`.
 *
 * `wallClock` is what an `<input type="datetime-local">` produces:
 * "2026-02-12T19:00", with no zone attached.
 *
 * The method is to guess an instant, ask what that instant reads as in the
 * target zone, and shift by the difference. One correction is enough for
 * every real zone, because zone offsets are fixed within the hours around
 * any instant — a second pass would only matter for an offset change larger
 * than the offset itself, which no zone has.
 *
 * Throws on input that is not a wall-clock reading, rather than returning
 * an Invalid Date that a caller would happily store.
 */
export function instantFromWallClock(wallClock: string, timeZone: string): Date {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(wallClock);
  if (!match) {
    throw new RangeError(`not a wall-clock reading: ${wallClock}`);
  }
  const [, year, month, day, hour, minute] = match.map(Number) as unknown as number[];

  // Read the parts as if they were UTC, then correct.
  const guess = Date.UTC(year!, month! - 1, day!, hour!, minute!);
  const readBack = wallClockPartsInZone(new Date(guess), timeZone);
  const drift = readBack - guess;
  return new Date(guess - drift);
}

/**
 * What `instant` reads as on the clock in `timeZone`, expressed as the
 * epoch milliseconds those same digits would mean in UTC. Only the
 * difference between this and the instant is ever used, which is the zone's
 * offset at that instant.
 */
function wallClockPartsInZone(instant: Date, timeZone: string): number {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone,
    hour12: false,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).formatToParts(instant);

  const field = (type: string) => Number(parts.find((p) => p.type === type)?.value ?? "0");
  // Intl renders midnight as hour 24 in some engines; both mean the same
  // instant, and 24 would roll the date forward if it were passed through.
  const hour = field("hour") % 24;
  return Date.UTC(field("year"), field("month") - 1, field("day"), hour, field("minute"), field("second"));
}

/**
 * The reader's own zone, which is the right default for somebody scheduling
 * something where they are.
 */
export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

/**
 * The wall-clock reading an `<input type="datetime-local">` needs to show
 * `instant` as it reads in `timeZone`. The inverse of instantFromWallClock,
 * so an edit form can round-trip a stored instant without drift.
 */
export function wallClockFromInstant(instant: Date, timeZone: string): string {
  const asUTC = new Date(wallClockPartsInZone(instant, timeZone));
  return asUTC.toISOString().slice(0, 16);
}
