export function ModelSwitch({ checked, busy, onChange, children }: {
  checked: boolean; busy: boolean; onChange: (value: boolean) => void; children: string;
}) {
  return <label className="model-switch">
    <span>{children}</span>
    <input type="checkbox" role="switch" checked={checked} disabled={busy} aria-busy={busy}
      onChange={(event) => onChange(event.target.checked)} />
    <span className="model-switch-track" aria-hidden="true" />
  </label>;
}
