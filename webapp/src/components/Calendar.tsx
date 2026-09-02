import { For, Show, createMemo, type JSX } from "solid-js";
import { A } from "@solidjs/router";
import type { Event } from "~/gen/types.gen";
import { useI18n } from "~/i18n";
import { addMonths, buildCells, groupByDay, ymd } from "~/lib/month";

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
 */
export function Calendar(props: {
  events: readonly Event[];
  year: number;
  month: number;
  onMove: (year: number, month: number) => void;
  loading?: boolean;
  /** Controls that belong to the calendar, rendered in its header. */
  controls?: JSX.Element;
}): JSX.Element {
  const { t, dateTime, time } = useI18n();

  const today = new Date();
  const cells = createMemo(() => buildCells(props.year, props.month, ymd(today)));
  const byDay = createMemo(() => groupByDay(props.events));

  const monthLabel = () =>
    new Intl.DateTimeFormat(undefined, { month: "long", year: "numeric" }).format(
      new Date(props.year, props.month, 1),
    );

  const move = (delta: number) => {
    const next = addMonths(props.year, props.month, delta);
    props.onMove(next.year, next.month);
  };

  // Monday first, as the grid is. The names come from the browser rather
  // than a catalog: they are the reader's locale's, not this app's.
  const weekdayNames = () => {
    const format = new Intl.DateTimeFormat(undefined, { weekday: "short" });
    return Array.from({ length: 7 }, (_, i) => {
      // 2026-01-05 was a Monday; adding i walks the week from there.
      return format.format(new Date(2026, 0, 5 + i));
    });
  };

  return (
    <section class="calendar" aria-label={t("calendar.label")}>
      <header class="calendar__head">
        <div class="calendar__title">
          <h2>{monthLabel()}</h2>
          <div class="calendar__nav">
            <button type="button" class="secondary compact" onClick={() => move(-1)}>
              {t("calendar.previous")}
            </button>
            <button
              type="button"
              class="secondary compact"
              onClick={() => props.onMove(today.getFullYear(), today.getMonth())}
            >
              {t("calendar.today")}
            </button>
            <button type="button" class="secondary compact" onClick={() => move(1)}>
              {t("calendar.next")}
            </button>
          </div>
        </div>
        <Show when={props.controls}>
          <div class="calendar__controls">{props.controls}</div>
        </Show>
      </header>

      {/*
        The count is announced rather than only drawn: paging a month is a
        change a sighted reader sees at a glance and a screen-reader user is
        told nothing about.
      */}
      <p class="visually-hidden" role="status" aria-live="polite">
        {props.loading
          ? t("common.loading")
          : t("calendar.announced", { month: monthLabel(), count: props.events.length })}
      </p>

      <div class="calendar__weekdays" aria-hidden="true">
        <For each={weekdayNames()}>{(name) => <div>{name}</div>}</For>
      </div>

      <ol class="calendar__grid">
        <For each={cells()}>
          {(cell) => {
            const events = () => byDay().get(cell.ymd) ?? [];
            return (
              <li
                class="calendar__cell"
                classList={{
                  "calendar__cell--outside": !cell.inMonth,
                  "calendar__cell--weekend": cell.isWeekend,
                  "calendar__cell--today": cell.isToday,
                }}
              >
                {/*
                  The full date is in the heading for a reader who cannot see
                  which column they are in; the number alone is what is drawn.
                */}
                <h3 class="calendar__day">
                  <span aria-hidden="true">{cell.day}</span>
                  <span class="visually-hidden">
                    {dateTime(new Date(`${cell.ymd}T12:00:00Z`)).split(",")[0]}
                  </span>
                  <Show when={cell.isToday}>
                    <span class="visually-hidden"> {t("calendar.todayLabel")}</span>
                  </Show>
                </h3>
                <ul class="calendar__events">
                  <For each={events()}>
                    {(event) => (
                      <li>
                        <A href={`/events/${event.id}`} class="calendar__event">
                          <span class="calendar__time">
                            {time(event.startsAt, event.timezone)}
                          </span>
                          <span class="calendar__event-title">{event.title}</span>
                          {/* The zone is always shown: an event is where it
                              is, and a reader elsewhere needs to know that
                              this time is not theirs. */}
                          <span class="visually-hidden"> ({event.timezone})</span>
                        </A>
                      </li>
                    )}
                  </For>
                </ul>
              </li>
            );
          }}
        </For>
      </ol>
    </section>
  );
}
