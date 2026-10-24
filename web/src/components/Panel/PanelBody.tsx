import type { ReactNode } from "react";

interface PanelBodyProps {
  children: ReactNode;
  className?: string;
}

export function PanelBody({ children, className = "" }: PanelBodyProps) {
  return (
    <div
      className={`flex-1 flex flex-col overflow-auto px-5 py-4 min-h-0 ${className}`.trim()}
    >
      {children}
    </div>
  );
}
