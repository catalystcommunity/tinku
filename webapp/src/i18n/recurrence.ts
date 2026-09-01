// Turning a structured recurrence rule into a sentence.
//
// The server never sends a described rule, only the rule itself. That is
// what makes the description translatable: "the second Thursday of every
// month" and its equivalent in another language do not put the ordinal, the
// weekday and the period in the same order, and a server that sent English
// would have decided the order for everybody.
import type { RecurrenceRule } from "~/gen/types.gen";
import type { MessageKey } from "./en-US";

type Translate = (key: MessageKey, values?: Record<string, string | number>) => string;

/**
 * describeRecurrence renders a rule, and optionally the local clock time it
 * happens at.
 */
export function describeRecurrence(
  t: Translate,
  rule: RecurrenceRule,
  startTime?: string,
): string {
  const sentence = describeRule(t, rule);
  if (!startTime) return sentence;
  return t("recurrence.at", { rule: sentence, time: startTime });
}

function describeRule(t: Translate, rule: RecurrenceRule): string {
  const weekday = rule.weekday ? t(`weekday.${rule.weekday}` as MessageKey) : "";
  const interval = Number(rule.interval ?? 1) || 1;

  if (rule.freq === "weekly") {
    return interval === 1
      ? t("recurrence.weekly", { weekday })
      : t("recurrence.weeklyInterval", { interval, weekday });
  }

  // A day-of-month rule has no weekday and no ordinal, so it is its own
  // sentence rather than a variant of the weekday one.
  if (rule.dayOfMonth != null) {
    return t("recurrence.dayOfMonth", { day: Number(rule.dayOfMonth) });
  }

  const ordinal = t(`recurrence.ordinal.${rule.ordinal ?? 1}` as MessageKey);
  switch (rule.freq) {
    case "quarterly":
      return t("recurrence.quarterly", { ordinal, weekday });
    case "yearly":
      return t("recurrence.yearly", { ordinal, weekday });
    default:
      return interval === 1
        ? t("recurrence.monthly", { ordinal, weekday })
        : t("recurrence.monthlyInterval", { ordinal, weekday, interval });
  }
}
