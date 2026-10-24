import { useId, useState, type ReactNode } from "react";

interface RailSectionProps {
  title: string;
  count?: number;
  collapsible?: boolean;
  defaultOpen?: boolean;
  children: ReactNode;
}

/** One labelled group in the left rail: rooms, direct channels, pairings, agents. */
export function RailSection({ title, count, collapsible = false, defaultOpen = true, children }: RailSectionProps) {
  const [open, setOpen] = useState(defaultOpen);
  const id = useId();
  const label = (
    <>
      <span>{title}</span>
      {count !== undefined ? <span className="text-faint font-normal">{count}</span> : null}
    </>
  );

  return (
    <section aria-labelledby={id} className="mb-4">
      <h2
        id={id}
        className="flex items-center gap-2 px-2 mb-1 font-mono text-[11px] font-semibold uppercase tracking-[0.08em] text-mute"
      >
        {collapsible ? (
          <button
            type="button"
            className="flex items-center gap-2 cursor-pointer hover:text-ink"
            aria-expanded={open}
            onClick={() => setOpen((value) => !value)}
          >
            <span aria-hidden="true" className="text-faint">{open ? "▾" : "▸"}</span>
            {label}
          </button>
        ) : (
          label
        )}
      </h2>
      {open ? children : null}
    </section>
  );
}
