import type { Event } from "~/gen/types.gen";
import { dateTime, t, time } from "~/i18n";
import { addMonths, buildCells, groupByDay, ymd } from "~/lib/month";
import { fill, h, type Child } from "~/ui/dom";
import { button } from "./Field";

/**
 * A read-only month grid.
 *
 * Read-only on purpose: scheduling happens on the gathering that owns the
 * event, where the permission is, and a calendar that could create things
 * would have to answer "under which gathering?" for every empty square.
 *
 * The placement rule is the domain's: an event sits on the day it happens
 * IN ITS OWN TIMEZONE. A 19:00 climb in Denver is on Thursday for everybody,
 * including a reader in Tokyo for whom that instant is Friday — see
 * lib/month.ts.
 *
 * Make it one time in `build()`. `controls` are elements that belong to the
 * calendar, put in its header one time, so a render never replaces them.
 * Call `update()` with each new state: the grid is made again only when the
 * month or the events change.
 */
export function calendar(opts: {
  onMove: (year: number, month: number) => void;
  /** Controls that belong to the calendar, put in its header. */
  controls?: Child;
}): {
  el: HTMLElement;
  update(props: { events: readonly Event[]; year: number; month: number; loading?: boolean }): void;
} {
  const today = new Date();
  let year = today.getFullYear();
  let month = today.getMonth();

  const monthLabel = () =>
    new Intl.DateTimeFormat(undefined, { month: "long", year: "numeric" }).format(new Date(year, month, 1));

  const move = (delta: number) => {
    const next = addMonths(year, month, delta);
    opts.onMove(next.year, next.month);
  };

  // Monday first, as the grid is. The names come from the browser rather
  // than a catalog: they are the reader's locale's, not this app's.
  const weekdayFormat = new Intl.DateTimeFormat(undefined, { weekday: "short" });
  // 2026-01-05 was a Monday; adding i walks the week from there.
  const weekdayNames = Array.from({ length: 7 }, (_, i) => weekdayFormat.format(new Date(2026, 0, 5 + i)));

  const heading = h("h2");
  // The count is announced rather than only drawn: paging a month is a
  // change a sighted reader sees at a glance and a screen-reader user is
  // told nothing about.
  const status = h("p", { class: "visually-hidden", role: "status", "aria-live": "polite" });
  const grid = h("ol", { class: "calendar__grid" });

  const el = h(
    "section",
    { class: "calendar", "aria-label": t("calendar.label") },
    h(
      "header",
      { class: "calendar__head" },
      h(
        "div",
        { class: "calendar__title" },
        heading,
        h(
          "div",
          { class: "calendar__nav" },
          button(t("calendar.previous"), { variant: "default", compact: true, onClick: () => move(-1) }),
          button(t("calendar.today"), {
            variant: "default",
            compact: true,
            onClick: () => opts.onMove(today.getFullYear(), today.getMonth()),
          }),
          button(t("calendar.next"), { variant: "default", compact: true, onClick: () => move(1) }),
        ),
      ),
      opts.controls ? h("div", { class: "calendar__controls" }, opts.controls) : null,
    ),
    status,
    h("div", { class: "calendar__weekdays", "aria-hidden": "true" }, weekdayNames.map((name) => h("div", null, name))),
    grid,
  );

  let shownKey = "";
  let shownEvents: readonly Event[] | null = null;

  const drawGrid = (events: readonly Event[]) => {
    const byDay = groupByDay(events);
    const cells = buildCells(year, month, ymd(today));
    fill(
      grid,
      cells.map((cell) => {
        const classes = ["calendar__cell"];
        if (!cell.inMonth) classes.push("calendar__cell--outside");
        if (cell.isWeekend) classes.push("calendar__cell--weekend");
        if (cell.isToday) classes.push("calendar__cell--today");
        return h(
          "li",
          { class: classes.join(" ") },
          // The full date is in the heading for a reader who cannot see
          // which column they are in; the number alone is what is drawn.
          h(
            "h3",
            { class: "calendar__day" },
            h("span", { "aria-hidden": "true" }, String(cell.day)),
            h("span", { class: "visually-hidden" }, dateTime(new Date(`${cell.ymd}T12:00:00Z`)).split(",")[0]),
            cell.isToday ? h("span", { class: "visually-hidden" }, ` ${t("calendar.todayLabel")}`) : null,
          ),
          h(
            "ul",
            { class: "calendar__events" },
            (byDay.get(cell.ymd) ?? []).map((event) =>
              h(
                "li",
                null,
                h(
                  "a",
                  { href: `/events/${event.id}`, class: "calendar__event" },
                  h("span", { class: "calendar__time" }, time(event.startsAt, event.timezone)),
                  h("span", { class: "calendar__event-title" }, event.title),
                  // The zone is always shown: an event is where it is, and a
                  // reader elsewhere needs to know that this time is not theirs.
                  h("span", { class: "visually-hidden" }, ` (${event.timezone})`),
                ),
              ),
            ),
          ),
        );
      }),
    );
  };

  return {
    el,
    update(props) {
      year = props.year;
      month = props.month;
      const label = monthLabel();
      if (heading.textContent !== label) heading.textContent = label;
      const announced = props.loading
        ? t("common.loading")
        : t("calendar.announced", { month: label, count: props.events.length });
      if (status.textContent !== announced) status.textContent = announced;
      const key = `${year}-${month}`;
      if (key === shownKey && props.events === shownEvents) return;
      shownKey = key;
      shownEvents = props.events;
      drawGrid(props.events);
    },
  };
}
