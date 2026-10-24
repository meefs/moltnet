import type { PairingDeliveryStatus } from "../../../lib/types";
import { DetailRow } from "../../DetailRow";

export function DeliveryStatus({ delivery }: { delivery: PairingDeliveryStatus }) {
  return (
    <section aria-label="Outbound delivery">
      <div className="text-[11px] tracking-[0.12em] text-green mt-1 mb-1.5">
        [ OUTBOUND DELIVERY ]
      </div>
      <div className="grid gap-2 text-xs mb-4">
        <DetailRow label="pending messages" value={String(delivery.pending)} />
        <DetailRow
          label="oldest pending"
          value={delivery.pending > 0 ? formatPendingAge(delivery.oldest_pending_age_seconds) : "—"}
        />
        <DetailRow label="dropped messages" value={String(delivery.dropped)} />
        {delivery.last_error ? (
          <DetailRow label="last delivery error" value={delivery.last_error} />
        ) : null}
      </div>
    </section>
  );
}

function formatPendingAge(seconds: number): string {
  const age = Math.max(0, Math.floor(seconds));
  if (age < 60) return `${age}s`;
  if (age < 3_600) return `${Math.floor(age / 60)}m`;
  if (age < 86_400) return `${Math.floor(age / 3_600)}h`;
  return `${Math.floor(age / 86_400)}d`;
}
