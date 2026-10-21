// Display formatting for the reader's dates, copied from frostmail's
// app/src/lib/format.ts at commit 4328cb7 (2026-10-21).

/**
 * parseTime reads an RFC 3339 time (docs/specs/api.md, Types) as a Date,
 * and answers null for an empty or unreadable one.
 */
export function parseTime(value: string): Date | null {
  if (value === "") return null;
  const time = Date.parse(value);
  return Number.isNaN(time) ? null : new Date(time);
}

/**
 * formatListDate shows a list date by the local calendar days between date
 * and now: the time for the same day, "Yesterday" for the day before, the
 * weekday for 2 to 6 days before, and the short numeric date otherwise.
 */
export function formatListDate(date: Date, now: Date, locale?: string): string {
  const midnight = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const daysBefore = Math.round((midnight(now) - midnight(date)) / 86_400_000);
  if (daysBefore === 0) {
    return date.toLocaleTimeString(locale, { hour: "numeric", minute: "2-digit" });
  }
  if (daysBefore === 1) return "Yesterday";
  if (daysBefore >= 2 && daysBefore <= 6) {
    return date.toLocaleDateString(locale, { weekday: "long" });
  }
  return date.toLocaleDateString(locale, { year: "2-digit", month: "numeric", day: "numeric" });
}

/** formatHeaderDate formats a reader header date as a long date with a short time. */
export function formatHeaderDate(date: Date, locale?: string): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: "long", timeStyle: "short" }).format(date);
}

/** listDate formats a time string for a list row, or "" when it has none. */
export function listDate(value: string, now: Date = new Date(), locale?: string): string {
  const date = parseTime(value);
  return date === null ? "" : formatListDate(date, now, locale);
}

/** headerDate formats a time string for the reader's header, or "" when it has none. */
export function headerDate(value: string, locale?: string): string {
  const date = parseTime(value);
  return date === null ? "" : formatHeaderDate(date, locale);
}
