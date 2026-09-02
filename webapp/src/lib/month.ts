/**
 * Month-grid arithmetic for the calendar. No DOM, no Solid — dates and
 * events in, grid cells out, so the awkward parts are unit-testable on
 * their own.
 *
 * The one decision that matters here: an event is placed on the day it
 * happens IN ITS OWN TIMEZONE, not in the reader's. A climb at 19:00 in
 * Denver belongs on Thursday for everybody looking at it, including
 * somebody in Tokyo for whom that instant is Friday afternoon. This is the
 * same rule the rest of the client follows when it prints a time, and it is
 * the reason this module never calls getDate() on a raw Date.
 */

/** One cell of the six-week grid. */
export interface Cell {
  /** The date this cell stands for, as YYYY-MM-DD. */
  ymd: string;
  /** Day of the month, for the label. */
  day: number;
  /** False for the leading and trailing days of the neighbouring months. */
  inMonth: boolean;
  isWeekend: boolean;
  isToday: boolean;
}

/** The minimum an event needs for the grid to place it. */
export interface Placeable {
  startsAt: Date | string;
  timezone: string;
}

/**
 * The local date of an instant in a named zone, as YYYY-MM-DD.
 *
 * Intl is what knows the offset on that date, daylight saving included —
 * doing this arithmetic by hand is how an event lands on the wrong day for
 * one week in November.
 */
export function ymdInZone(instant: Date | string, timeZone: string): string {
  const date = instant instanceof Date ? instant : new Date(instant);
  try {
    // en-CA formats as YYYY-MM-DD, which is the shape we want to compare
    // and sort as text.
    return new Intl.DateTimeFormat("en-CA", {
      timeZone,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
    }).format(date);
  } catch {
    // An unknown zone must not take the whole calendar down with it. UTC is
    // the honest fallback: it is what the instant is stored as.
    return new Intl.DateTimeFormat("en-CA", {
      timeZone: "UTC",
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
    }).format(date);
  }
}

/** YYYY-MM-DD for a local Date, used for "today" and for building cells. */
export function ymd(date: Date): string {
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

/** The Monday on or before a date. */
export function startOfWeek(date: Date): Date {
  const weekday = date.getDay(); // 0 = Sunday
  const shift = weekday === 0 ? -6 : 1 - weekday;
  const out = new Date(date);
  out.setDate(date.getDate() + shift);
  return out;
}

/**
 * The 42 cells of a Monday-first month grid.
 *
 * Always six rows, never five: a grid that changes height as you page
 * through the year makes the controls above it jump under the pointer.
 */
export function buildCells(year: number, month: number, todayYmd: string): Cell[] {
  const gridStart = startOfWeek(new Date(year, month, 1));
  const cells: Cell[] = [];
  for (let i = 0; i < 42; i += 1) {
    const date = new Date(gridStart);
    date.setDate(gridStart.getDate() + i);
    const key = ymd(date);
    cells.push({
      ymd: key,
      day: date.getDate(),
      inMonth: date.getMonth() === month,
      isWeekend: date.getDay() === 0 || date.getDay() === 6,
      isToday: key === todayYmd,
    });
  }
  return cells;
}

/**
 * Group events by the day they fall on in their own zone.
 *
 * The order within a day is the order the caller supplied, which is the
 * server's — by start time — so nothing here re-sorts and nothing has to
 * agree with the server about how to.
 */
export function groupByDay<T extends Placeable>(events: readonly T[]): Map<string, T[]> {
  const byDay = new Map<string, T[]>();
  for (const event of events) {
    const key = ymdInZone(event.startsAt, event.timezone);
    const existing = byDay.get(key);
    if (existing) {
      existing.push(event);
    } else {
      byDay.set(key, [event]);
    }
  }
  return byDay;
}

/** The first instant of a month, for the window a query asks the server for. */
export function monthStart(year: number, month: number): Date {
  return new Date(year, month, 1, 0, 0, 0, 0);
}

/**
 * The last instant of the month, exclusive-ish.
 *
 * The window is widened by a day at each end because the grid shows the
 * neighbouring months' edge days, and because a zone can push an event into
 * the day next door — a query cut exactly at midnight local would leave a
 * visible cell empty that has something in it.
 */
export function monthWindow(year: number, month: number): { from: Date; to: Date } {
  const from = startOfWeek(monthStart(year, month));
  from.setDate(from.getDate() - 1);
  const to = new Date(from);
  to.setDate(from.getDate() + 44);
  return { from, to };
}

/** Month arithmetic that does not roll the day. */
export function addMonths(year: number, month: number, delta: number): { year: number; month: number } {
  const total = year * 12 + month + delta;
  return { year: Math.floor(total / 12), month: ((total % 12) + 12) % 12 };
}
