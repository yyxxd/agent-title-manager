import { ChevronLeft, ChevronRight, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { asDate, keyOf, monthOf, offsetDay, offsetMonth, type DateRange } from './dates';

const weekdays = ['一', '二', '三', '四', '五', '六', '日'];

export function DatePopover({
  today,
  value,
  onSelect,
  onSelectToday,
  onClose,
}: {
  today: string;
  value: DateRange;
  onSelect: (range: DateRange) => void;
  onSelectToday: () => void;
  onClose: () => void;
}) {
  const [month, setMonth] = useState(() => monthOf(asDate(value.end <= today ? value.end : today)));
  const [pendingStart, setPendingStart] = useState<string | null>(null);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const first = month;
  const mondayBased = (first.getDay() + 6) % 7;
  const gridStart = offsetDay(first, -mondayBased);
  const cells = Array.from({ length: 42 }, (_, index) => {
    const day = offsetDay(gridStart, index);
    return {
      key: keyOf(day),
      dayNum: day.getDate(),
      inMonth: day.getMonth() === month.getMonth(),
    };
  });

  const maxMonth = monthOf(asDate(today));
  const canNextMonth = offsetMonth(month, 1).getTime() <= maxMonth.getTime();
  const title = `${month.getFullYear()} 年 ${month.getMonth() + 1} 月`;

  function handlePick(iso: string) {
    if (!pendingStart) {
      setPendingStart(iso);
      return;
    }
    const start = pendingStart <= iso ? pendingStart : iso;
    const end = pendingStart <= iso ? iso : pendingStart;
    onSelect({ start, end });
    onClose();
  }

  return (
    <div className="popover-panel" role="dialog" aria-label="选择统计日期或范围">
      <div className="cal-head">
        <button
          type="button"
          className="icon-btn"
          style={{ width: 26, height: 26 }}
          onClick={() => setMonth((m) => offsetMonth(m, -1))}
          aria-label="上个月"
        >
          <ChevronLeft size={14} aria-hidden="true" />
        </button>
        <span>{title}</span>
        <div style={{ display: 'inline-flex', alignItems: 'center', gap: 2 }}>
          <button
            type="button"
            className="icon-btn"
            style={{ width: 26, height: 26 }}
            disabled={!canNextMonth}
            onClick={() => setMonth((m) => offsetMonth(m, 1))}
            aria-label="下个月"
          >
            <ChevronRight size={14} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="icon-btn"
            style={{ width: 26, height: 26 }}
            onClick={onClose}
            aria-label="关闭日历"
          >
            <X size={14} aria-hidden="true" />
          </button>
        </div>
      </div>

      <div className="cal-grid">
        {weekdays.map((dow) => (
          <span className="cal-dow" key={dow}>
            {dow}
          </span>
        ))}
        {cells.map((cell) => {
          const disabled = cell.key > today;
          const isStartOrEnd = pendingStart
            ? cell.key === pendingStart
            : cell.key === value.start || cell.key === value.end;
          const inRange =
            !pendingStart && value.start !== value.end && cell.key > value.start && cell.key < value.end;
          const classes = [
            'cal-day',
            (!cell.inMonth || disabled) && 'muted',
            isStartOrEnd && 'selected',
            inRange && 'in-range',
          ]
            .filter(Boolean)
            .join(' ');
          return (
            <button
              key={cell.key}
              type="button"
              className={classes}
              disabled={disabled}
              onClick={() => handlePick(cell.key)}
            >
              {cell.dayNum}
            </button>
          );
        })}
      </div>

      <div className="cal-foot">
        <span className="tabular">
          {pendingStart
            ? `已选 ${pendingStart}，再点结束日`
            : value.start === value.end
              ? value.start
              : `${value.start} ~ ${value.end}`}
        </span>
        {pendingStart ? (
          <button
            type="button"
            className="btn btn-sm btn-ghost"
            onClick={() => {
              onSelect({ start: pendingStart, end: pendingStart });
              onClose();
            }}
          >
            仅看该日
          </button>
        ) : (
          <button
            type="button"
            className="btn btn-sm btn-ghost"
            onClick={() => {
              onSelectToday();
              onClose();
            }}
          >
            回到今日
          </button>
        )}
      </div>
    </div>
  );
}
