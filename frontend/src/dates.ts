export type DateRange = { start: string; end: string };

export const asDate = (key: string) => new Date(key + 'T12:00:00');
export const keyOf = (date: Date) => [date.getFullYear(), date.getMonth() + 1, date.getDate()]
  .map((part, index) => String(part).padStart(index === 0 ? 4 : 2, '0')).join('-');
export const monthOf = (date: Date) => new Date(date.getFullYear(), date.getMonth(), 1, 12);
export const offsetDay = (date: Date, days: number) => new Date(date.getFullYear(), date.getMonth(), date.getDate() + days, 12);
export const offsetMonth = (month: Date, months: number) => new Date(month.getFullYear(), month.getMonth() + months, 1, 12);
export const sameRange = (a: DateRange, b: DateRange) => a.start === b.start && a.end === b.end;

export function datePresets(today: string) {
  const date = asDate(today);
  const month = monthOf(date);
  const before = (days: number) => keyOf(offsetDay(date, -days));
  const endingToday = (start: string) => ({ start, end: today });
  return [
    { label: '今天', range: endingToday(today) },
    { label: '昨天', range: { start: before(1), end: before(1) } },
    { label: '近 7 天', range: endingToday(before(6)) },
    { label: '近 14 天', range: endingToday(before(13)) },
    { label: '近 30 天', range: endingToday(before(29)) },
    { label: '本月', range: endingToday(keyOf(month)) },
    { label: '上月', range: { start: keyOf(offsetMonth(month, -1)), end: keyOf(offsetDay(month, -1)) } },
  ];
}
