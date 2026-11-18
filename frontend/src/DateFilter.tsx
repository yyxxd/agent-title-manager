import { CalendarDays, ChevronLeft, ChevronRight } from 'lucide-react';
import { useId, useRef, useState, type KeyboardEvent } from 'react';
import { localDate } from './rpc';
import { asDate, datePresets, keyOf, monthOf, offsetDay, offsetMonth, sameRange, type DateRange } from './dates';

const weekdays = ['一', '二', '三', '四', '五', '六', '日'];
const format = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'long' });
function calendarDays(month: Date) {
  const start = offsetDay(month, -((month.getDay() + 6) % 7));
  const count = Math.ceil(((month.getDay() + 6) % 7 + offsetDay(offsetMonth(month, 1), -1).getDate()) / 7) * 7;
  return Array.from({ length: count }, (_, index) => offsetDay(start, index));
}

export function DateFilter({ range, preset, onChange }: { range: DateRange; preset?: string; onChange: (range: DateRange, preset?: string) => void }) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const [month, setMonth] = useState(() => monthOf(asDate(range.end)));
  const [focused, setFocused] = useState(range.end);
  const today = localDate();

  const presets = datePresets(today);
  const fullRange = range.start === range.end ? range.end : range.start + ' 至 ' + range.end;
  const compactRange = range.start === range.end ? range.end.replaceAll('-', '/') : range.start.replaceAll('-', '/') + ' – ' + range.end.replaceAll('-', '/');

  function moveFocus(key: string) {
    setFocused(key);
    requestAnimationFrame(() => panel.current?.querySelector<HTMLButtonElement>('[data-day="' + key + '"]')?.focus({ preventScroll: true }));
  }

  function choose(next: DateRange, label?: string) {
    onChange(next, label);
    panel.current?.hidePopover();
    trigger.current?.focus();
  }

  function changeMonth(direction: number) {
    const next = offsetMonth(month, direction);
    setMonth(next);
    setFocused(keyOf(next));
  }

  function onDateKey(event: KeyboardEvent<HTMLButtonElement>, date: Date) {
    const offsets: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 };
    const offset = offsets[event.key];
    if (offset === undefined) return;
    event.preventDefault();
    const next = offsetDay(date, offset);
    const key = keyOf(next);
    if (key > today) return;
    setMonth(monthOf(next));
    moveFocus(key);
  }

  return <div className="date-control">
    <button className="date-trigger" type="button" ref={trigger} popoverTarget={id} aria-label={'统计日期：' + (preset ? preset + '，' : '') + fullRange} title={fullRange}>
      <CalendarDays size={15} aria-hidden="true" />
      <span>{preset ?? compactRange}</span>
    </button>
    <div id={id} className="date-picker" popover="auto" ref={panel} role="dialog" aria-label="选择统计日期" onToggle={(event) => {
      if (event.currentTarget.matches(':popover-open')) {
        event.currentTarget.scrollTop = 0;
        setMonth(monthOf(asDate(range.end)));
        moveFocus(range.end);
      }
    }}>
      <div className="date-picker-layout">
        <div className="date-presets" role="group" aria-label="快捷日期">
          {presets.map(({ label, range: next }) => <button type="button" key={label}
            aria-pressed={label === preset && sameRange(next, range)}
            onClick={() => choose(next, label)}>{label}</button>)}
        </div>
        <div>
      <div className="calendar-heading">
        <button type="button" className="icon-button" onClick={() => changeMonth(-1)} aria-label="上个月"><ChevronLeft size={15} /></button>
        <strong aria-live="polite">{month.getFullYear()} 年 {month.getMonth() + 1} 月</strong>
        <button type="button" className="icon-button" onClick={() => changeMonth(1)} disabled={keyOf(offsetMonth(month, 1)) > today} aria-label="下个月"><ChevronRight size={15} /></button>
      </div>
      <div className="calendar-days">
        {weekdays.map(day => <span className="calendar-weekday" key={day} aria-hidden="true">{day}</span>)}
        {calendarDays(month).map(date => {
          const key = keyOf(date);
          return <button type="button" key={key} data-day={key} data-outside={date.getMonth() !== month.getMonth()}
            aria-current={key === today ? 'date' : undefined} aria-pressed={range.start === key && range.end === key}
            data-in-range={key >= range.start && key <= range.end}
            data-range-edge={key === range.start || key === range.end}
            aria-label={format.format(date)} disabled={key > today} tabIndex={key === focused ? 0 : -1}
            onKeyDown={event => onDateKey(event, date)} onClick={() => choose({ start: key, end: key })}>{date.getDate()}</button>;
        })}
      </div>
        </div>
      </div>
      <div className="calendar-footer" aria-live="polite">{fullRange}</div>
    </div>
  </div>;
}
