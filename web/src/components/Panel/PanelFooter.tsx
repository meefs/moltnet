import type { ReactNode } from "react";

interface PanelFooterProps {
  children: ReactNode;
  className?: string;
}

export function PanelFooter({ children, className = "" }: PanelFooterProps) {
  return (
    <div
      className={`pt-1.5 px-1 text-[11px] text-faint ${className}`.trim()}
    >
      {children}
    </div>
  );
}
