import { useEventStream } from "../../providers";

export function StreamStatus() {
  const { status, reason } = useEventStream();

  const label =
    status === "open"
      ? "live"
      : status === "unsupported"
        ? "no live stream"
      : status === "error"
        ? "reconnecting…"
        : "connecting…";

  const error = status === "error";
  const unsupported = status === "unsupported";
  const tone = error || unsupported ? "text-coral" : "text-green";
  const dotColor = error || unsupported ? "bg-coral" : "bg-green";
  const animate = status === "open" ? "animate-breathe" : "";

  return (
    <p
      className={`font-mono text-[12px] ${tone} inline-flex items-center gap-1.5 shrink-0`}
      title={reason ?? (status === "open" ? "live event stream connected" : undefined)}
    >
      <span className={`w-[7px] h-[7px] rounded-full ${dotColor} ${animate}`} />
      {label}
    </p>
  );
}
