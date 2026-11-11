// The i18n seam. Every string a person reads comes through `t()`; every
// date, time and number comes through the formatters here.
//
// en-US is the default and the fallback. Adding a locale means adding a
// catalog and one entry to `catalogs` — no component changes, because no
// component holds a string.
//
// What this deliberately does NOT do:
//
//   * It does not concatenate. A message with a value in it is one message
//     with a {placeholder}, so a translator controls word order.
//   * It does not implement pluralization with `n === 1`. Intl.PluralRules
//     picks the category for the active locale, and the catalog supplies a
//     key per category — English has two, and a language with six is a
//     catalog change rather than a code change.
//   * It does not format dates itself. Intl does that, with the active
//     locale and the event's own timezone where the domain calls for it.
//
// The locale is app state in the store (collection I18N). The shell watches
// it and builds the page again when it changes, so a component reads the
// functions below and never subscribes to the locale itself.
import { cell } from "~/state/store";
import { enUS, type Catalog, type MessageKey, type PluralKey } from "./en-US";

/** Locales this build carries. en-US is the source catalog. */
const catalogs: Record<string, Catalog> = {
  "en-US": enUS,
};

/** The locale used when nothing better is on offer. */
export const DEFAULT_LOCALE = "en-US";

/** The store collection of the active locale. */
export const I18N = "i18n";

type Values = Record<string, string | number>;

export interface I18nValue {
  locale: () => string;
  setLocale: (locale: string) => void;
  /** Look a message up and fill its placeholders. */
  t: (key: MessageKey, values?: Values) => string;
  /** Look a plural message up: `key` names the base, and the category
   *  suffix is chosen for the active locale. */
  plural: (key: PluralKey, count: number, values?: Values) => string;
  /** A date and time in the reader's locale. `timeZone` renders it in the
   *  event's own zone instead of the reader's — which is what "7pm local"
   *  means when the local in question is the organizer's. */
  dateTime: (value: Date | string, timeZone?: string) => string;
  /** A date with no time. */
  date: (value: Date | string, timeZone?: string) => string;
  /** A clock time with no date. */
  time: (value: Date | string, timeZone?: string) => string;
  /** A number, grouped as the locale groups numbers. */
  number: (value: number) => string;
}

/**
 * negotiate picks the best locale this build has for the browser's
 * preferences. It matches the full tag first ("en-GB"), then the language
 * ("en"), so a reader asking for a regional variant we do not carry still
 * gets their language rather than the default.
 */
export function negotiate(preferred: readonly string[], available = Object.keys(catalogs)): string {
  for (const tag of preferred) {
    const exact = available.find((candidate) => candidate.toLowerCase() === tag.toLowerCase());
    if (exact) return exact;
    const language = tag.split("-")[0]?.toLowerCase();
    const byLanguage = available.find(
      (candidate) => candidate.split("-")[0]?.toLowerCase() === language,
    );
    if (byLanguage) return byLanguage;
  }
  return DEFAULT_LOCALE;
}

/**
 * interpolate fills {placeholders}. A placeholder with no value is left as
 * it is rather than becoming "undefined": a visible {name} tells a
 * translator exactly which message is wrong, and an "undefined" does not.
 */
function interpolate(template: string, values?: Values): string {
  if (!values) return template;
  return template.replace(/\{(\w+)\}/g, (whole, name: string) =>
    name in values ? String(values[name]) : whole,
  );
}

const initialLocale = negotiate(
  typeof navigator === "undefined" ? [] : (navigator.languages ?? [navigator.language]),
);
const localeCell = cell<{ tag: string }>(I18N, "locale", { tag: initialLocale });

// The document's language has to track the active locale: it is what a
// screen reader uses to pick a voice, and what a browser uses to decide
// hyphenation and quotation marks.
function applyDocumentLanguage(tag: string): void {
  if (typeof document !== "undefined") {
    document.documentElement.lang = tag;
  }
}
applyDocumentLanguage(initialLocale);

const locale = () => localeCell.get().tag;

/** Change the active locale. The shell builds the page again. */
export function setLocale(tag: string): void {
  if (tag === locale()) return;
  applyDocumentLanguage(tag);
  formatters.clear();
  localeCell.set({ tag });
}

const lookup = (key: string): string =>
  ((catalogs[locale()] ?? enUS) as Record<string, string | undefined>)[key] ??
  (enUS as Record<string, string | undefined>)[key] ??
  key;

export const t = (key: MessageKey, values?: Values): string => interpolate(lookup(key), values);

export const plural = (key: PluralKey, count: number, values?: Values): string => {
  const category = pluralRules().select(count);
  // The category suffix first, then `_other` as the fallback: a catalog
  // that has not supplied a rarer category still renders a sentence.
  const template =
    lookup(`${key}_${category}`) !== `${key}_${category}` ? lookup(`${key}_${category}`) : lookup(`${key}_other`);
  return interpolate(template, { count, ...values });
};

// One formatter per (locale, options, zone). A list of fifty events asked
// for a new Intl.DateTimeFormat per card, and construction is the slow part.
const formatters = new Map<string, Intl.DateTimeFormat | Intl.NumberFormat | Intl.PluralRules>();

function cached<T extends Intl.DateTimeFormat | Intl.NumberFormat | Intl.PluralRules>(key: string, make: () => T): T {
  const full = `${locale()}|${key}`;
  let f = formatters.get(full) as T | undefined;
  if (!f) {
    f = make();
    formatters.set(full, f);
  }
  return f;
}

const pluralRules = () => cached("plural", () => new Intl.PluralRules(locale()));

const asDate = (value: Date | string) => (value instanceof Date ? value : new Date(value));

export const dateTime = (value: Date | string, timeZone?: string): string =>
  cached(`dt|${timeZone ?? ""}`, () =>
    new Intl.DateTimeFormat(locale(), { dateStyle: "medium", timeStyle: "short", timeZone }),
  ).format(asDate(value));

export const date = (value: Date | string, timeZone?: string): string =>
  cached(`d|${timeZone ?? ""}`, () => new Intl.DateTimeFormat(locale(), { dateStyle: "medium", timeZone })).format(
    asDate(value),
  );

export const time = (value: Date | string, timeZone?: string): string =>
  cached(`t|${timeZone ?? ""}`, () => new Intl.DateTimeFormat(locale(), { timeStyle: "short", timeZone })).format(
    asDate(value),
  );

export const number = (value: number): string =>
  cached("n", () => new Intl.NumberFormat(locale())).format(value);

const value: I18nValue = { locale, setLocale, t, plural, dateTime, date, time, number };

/** The i18n functions as one object. */
export function useI18n(): I18nValue {
  return value;
}

export type { MessageKey, PluralKey };
