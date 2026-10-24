import type { ReactNode } from "react";

export function PanelHeader({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3 h-14 shrink-0 px-5 border-b border-border">
      {children}
    </div>
  );
}
