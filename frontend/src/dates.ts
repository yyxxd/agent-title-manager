export type DateRange = { start: string; end: string };

export const ALL_TIME_START = '2020-01-01';

export const asDate = (key: string) => new Date(key + 'T12:00:00');

export const keyOf = (date: Date) =>
  [date.getFullYear(), date.getMonth() + 1, date.getDate()]
    .map((part, index) => String(part).padStart(index === 0 ? 4 : 2, '0'))
    .join('-');

export const monthOf = (date: Date) => new Date(date.getFullYear(), date.getMonth(), 1, 12);

export const offsetDay = (date: Date, days: number) =>
  new Date(date.getFullYear(), date.getMonth(), date.getDate() + days, 12);

export const offsetMonth = (month: Date, months: number) =>
  new Date(month.getFullYear(), month.getMonth() + months, 1, 12);

export const sameRange = (a: DateRange, b: DateRange) => a.start === b.start && a.end === b.end;

export function rangeDays(today: string, days: number): DateRange {
  const date = asDate(today);
  return { start: keyOf(offsetDay(date, -(days - 1))), end: today };
}

export function formatRangeLabel(range: DateRange): string {
  if (range.start === ALL_TIME_START) return `全部（截至 ${range.end}）`;
  if (range.start === range.end) return range.start;
  return `${range.start} 至 ${range.end}`;
}
