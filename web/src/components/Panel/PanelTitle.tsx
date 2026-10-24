import type { ReactNode } from "react";

export function PanelTitle({ children }: { children: ReactNode }) {
  return (
    <h1 className="text-[17px] font-semibold tracking-[-0.01em] truncate">{children}</h1>
  );
}
