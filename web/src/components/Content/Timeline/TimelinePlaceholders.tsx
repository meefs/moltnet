import { dayLabel } from "../../../lib/format";

export function DayDivider({ date }: { date: string }) {
  return (
    <div className="flex items-center gap-3 pt-4 pb-1" role="separator" aria-label={dayLabel(date)}>
      <span className="flex-1 border-t border-line-soft" />
      <span className="font-mono text-[11px] text-mute">{dayLabel(date)}</span>
      <span className="flex-1 border-t border-line-soft" />
    </div>
  );
}

export function EmptyPane({ text }: { text: string }) {
  return (
    <section className="grid place-items-center min-h-0 text-faint text-[14px]">
      <p>{text}</p>
    </section>
  );
}

/** A shared-room membership change, as a centered muted system line. */
export function ControlEventLine({ text, date }: { text: string; date: string }) {
  const at = new Date(date);
  const title = Number.isNaN(at.valueOf()) ? date : at.toLocaleString();
  return (
    <p className="py-1.5 text-center font-mono text-[11px] text-faint" role="note" data-control-event="">
      <time dateTime={date} title={title}>
        {text}
      </time>
    </p>
  );
}
