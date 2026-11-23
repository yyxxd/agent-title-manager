import { useLayoutEffect, useRef, type HTMLAttributes } from 'react';

type SegmentedProps = HTMLAttributes<HTMLElement> & {
  as?: 'nav' | 'div';
  value?: string;
};

export function Segmented({ as: Tag = 'div', value, className = 'segmented', children, ...props }: SegmentedProps) {
  const groupRef = useRef<HTMLElement>(null);
  const markerRef = useRef<HTMLSpanElement>(null);

  useLayoutEffect(() => {
    const group = groupRef.current;
    const marker = markerRef.current;
    if (!group || !marker) return;

    const options = [...group.querySelectorAll<HTMLElement>('[data-segment]')];
    const selected = options.find((option) => option.dataset.segment === value);
    marker.hidden = !selected;
    if (!selected) return;

    const update = () => {
      const target = selected.getBoundingClientRect();
      const bounds = group.getBoundingClientRect();
      marker.style.setProperty('--segment-x', `${target.left - bounds.left - group.clientLeft}px`);
      marker.style.setProperty('--segment-y', `${target.top - bounds.top - group.clientTop}px`);
      marker.style.width = `${target.width}px`;
      marker.style.height = `${target.height}px`;
    };
    update();
    const observer = new ResizeObserver(update);
    observer.observe(group);
    options.forEach((option) => observer.observe(option));
    return () => observer.disconnect();
  }, [value]);

  return (
    <Tag {...props} className={`segment-group ${className}`} ref={(element) => { groupRef.current = element; }}>
      <span className="segment-marker" ref={markerRef} aria-hidden="true" hidden />
      {children}
    </Tag>
  );
}
