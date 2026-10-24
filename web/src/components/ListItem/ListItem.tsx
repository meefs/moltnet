import type { ReactNode } from "react";

interface ListItemProps {
  active?: boolean;
  showMarker?: boolean;
  marker?: ReactNode;
  markerClassName?: string;
  onClick?: () => void;
  title: ReactNode;
  subtitle?: ReactNode;
  trailing?: ReactNode;
}

export function ListItem({
  active = false,
  showMarker = true,
  marker,
  markerClassName = "",
  onClick,
  title,
  subtitle,
  trailing,
}: ListItemProps) {
  const containerClass = [
    "block w-full text-left rounded-md px-2 py-1.5 cursor-pointer text-[14px] transition-colors",
    active
      ? "bg-panel-2 text-ink"
      : "text-sub hover:bg-panel-2/60 hover:text-ink",
  ].join(" ");

  return (
    <button
      type="button"
      className={containerClass}
      onClick={onClick}
      aria-current={active ? "page" : undefined}
    >
      <div className="flex justify-between items-center gap-2">
        <span className="flex-1 min-w-0 flex items-center gap-2">
          {showMarker && marker !== undefined ? (
            <span aria-hidden="true" className={`shrink-0 inline-flex ${markerClassName}`}>
              {marker}
            </span>
          ) : null}
          <span className="min-w-0">
            <span className={`block truncate ${active ? "font-medium" : ""}`}>{title}</span>
            {subtitle !== undefined ? (
              <span className="block truncate text-faint text-[12px]">{subtitle}</span>
            ) : null}
          </span>
        </span>
        {trailing !== undefined ? (
          <span className="font-mono text-faint text-[11px] shrink-0">{trailing}</span>
        ) : null}
      </div>
    </button>
  );
}
