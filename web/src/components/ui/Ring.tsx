
export default function Ring({ pct, className = 'cring', label = 'complete' }: { pct: number; className?: string; label?: string }) {
  const offset = 264 - (264 * pct) / 100;
  return (
    <div className={className}>
      <svg viewBox="0 0 100 100" aria-hidden="true">
        <circle cx="50" cy="50" r="42" fill="none" stroke="var(--surface-3)" strokeWidth="9" />
        <circle cx="50" cy="50" r="42" fill="none" stroke="var(--accent)" strokeWidth="9" strokeLinecap="round" strokeDasharray="264" strokeDashoffset={offset} />
      </svg>
      <div style={{ textAlign: 'center' }}>
        <div className="v">{pct}%</div>
        <div className="l">{label}</div>
      </div>
    </div>
  );
}
