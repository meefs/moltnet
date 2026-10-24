export function SectionLabel({ title, count }: { title: string; count?: number }) {
  return (
    <h2 className="flex items-center gap-2 mt-6 mb-2 font-mono text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">
      {title}
      {count !== undefined ? <span className="text-faint font-normal">{count}</span> : null}
    </h2>
  );
}
