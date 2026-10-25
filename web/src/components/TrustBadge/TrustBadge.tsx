import type { TrustBadgeSpec, TrustTone } from "../../lib/trust";

// Same chip shape as the "via <pairing>" provenance chip in the timeline.
const TONES: Record<TrustTone, string> = {
  green: "text-green border-green/30",
  amber: "text-amber/75 border-amber/25",
  muted: "text-faint border-line",
  coral: "text-coral border-coral/40",
};

/** A small provenance chip; renders nothing for a null spec. */
export function TrustBadge({ spec }: { spec: TrustBadgeSpec | null }) {
  if (!spec) return null;
  return (
    <span
      className={`shrink-0 font-mono text-[11px] border rounded px-1.5 py-px normal-case tracking-normal font-normal ${TONES[spec.tone]}`}
      title={spec.title}
      data-trust={spec.tone}
    >
      {spec.label}
    </span>
  );
}
