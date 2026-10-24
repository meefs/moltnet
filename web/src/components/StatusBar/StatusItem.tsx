import type { ReactNode } from "react";

interface StatusItemProps {
  label?: string;
  value: ReactNode;
  tone?: "default" | "good" | "ink" | "warn";
}

export function StatusItem({ label, value, tone = "default" }: StatusItemProps) {
  const valueClass =
    tone === "good"
      ? "text-green"
      : tone === "ink"
        ? "text-sub"
        : tone === "warn"
          ? "text-coral"
          : "text-mute";

  return (
    <span className="inline-flex gap-1">
      {label ? <span className="text-faint">{label}</span> : null}
      <span className={valueClass}>{value}</span>
    </span>
  );
}
