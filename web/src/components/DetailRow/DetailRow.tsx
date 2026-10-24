import type { ReactNode } from "react";

interface DetailRowProps {
  label: string;
  value: ReactNode;
}

export function DetailRow({ label, value }: DetailRowProps) {
  return (
    <div className="grid grid-cols-[140px_1fr] items-baseline gap-3 py-1 border-b border-line-soft last:border-0">
      <span className="text-faint font-mono text-[12px]">{label}</span>
      <span className="text-sub text-[13.5px] break-all">{value}</span>
    </div>
  );
}
