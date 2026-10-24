import type { PairingDeliveryStatus } from "../../../lib/types";
import { DetailRow } from "../../DetailRow";
import { SectionLabel } from "../../SectionLabel";

export function DeliveryStatus({ delivery }: { delivery: PairingDeliveryStatus }) {
  const rejected = delivery.rejected ?? 0;
  return (
    <section aria-label="Outbound delivery">
      <SectionLabel title="Outbound delivery" />
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3 max-w-3xl">
        <Stat label="pending messages" value={String(delivery.pending)} warn={delivery.pending > 0} />
        <Stat
          label="oldest pending"
          value={delivery.pending > 0 ? formatPendingAge(delivery.oldest_pending_age_seconds) : "—"}
          warn={delivery.pending > 0}
        />
        <Stat label="rejected messages" value={String(rejected)} bad={rejected > 0} />
        <Stat label="dropped messages" value={String(delivery.dropped)} bad={delivery.dropped > 0} />
      </div>
      {delivery.last_rejection_reason || delivery.last_error ? (
        <div className="max-w-3xl mt-3">
          {delivery.last_rejection_reason ? (
            <DetailRow label="last rejection reason" value={delivery.last_rejection_reason} />
          ) : null}
          {delivery.last_error ? (
            <DetailRow label="last delivery error" value={delivery.last_error} />
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

function Stat({ label, value, warn, bad }: { label: string; value: string; warn?: boolean; bad?: boolean }) {
  const tone = bad ? "text-coral" : warn ? "text-amber" : "text-ink";
  return (
    <div role="group" aria-label={label} className="rounded-lg border border-line bg-panel px-4 py-3">
      <div className={`text-[24px] font-semibold tracking-[-0.02em] tabular-nums ${tone}`}>{value}</div>
      <div className="font-mono text-[11px] text-faint">{label}</div>
    </div>
  );
}

function formatPendingAge(seconds: number): string {
  const age = Math.max(0, Math.floor(seconds));
  if (age < 60) return `${age}s`;
  if (age < 3_600) return `${Math.floor(age / 60)}m`;
  if (age < 86_400) return `${Math.floor(age / 3_600)}h`;
  return `${Math.floor(age / 86_400)}d`;
}
