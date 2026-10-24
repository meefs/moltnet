import { useEffect, useState } from "react";
import { useLatency } from "../../hooks/useLatency";
import { useNetwork } from "../../hooks/useNetwork";
import { formatClockUTC } from "../../lib/format";
import {
  capabilityText,
  supportsDirectMessages,
  supportsEventStream,
} from "../../lib/capabilities";
import { StatusItem } from "./StatusItem";

export function StatusBar() {
  const { data: network } = useNetwork();
  const latency = useLatency();
  const [clock, setClock] = useState(() => formatClockUTC(new Date()));

  useEffect(() => {
    const id = setInterval(() => setClock(formatClockUTC(new Date())), 1000);
    return () => clearInterval(id);
  }, []);

  const ingressOn = network?.capabilities?.human_ingress === true;
  const eventStreamEnabled = supportsEventStream(network);
  const directMessagesEnabled = supportsDirectMessages(network);

  return (
    <footer className="flex justify-between gap-6 h-8 items-center px-4 border-t border-border font-mono text-[11px] text-faint bg-bg tabular-nums overflow-hidden whitespace-nowrap">
      <div className="flex gap-5 items-center min-w-0">
        <StatusItem label="network:" value={network?.id ?? "—"} />
        <StatusItem label="viewer:" value={
          network?.console?.viewer?.kind === "anonymous"
            ? "public" : network?.console?.viewer?.id ?? "—"
        } />
        <StatusItem
          label="latency:"
          value={latency != null ? `${latency}ms` : "—"}
        />
      </div>
      <div className="flex gap-5 items-center">
        <StatusItem
          label="stream:"
          value={network ? capabilityText(network.capabilities?.event_stream) : "—"}
          tone={network ? (eventStreamEnabled ? "good" : "warn") : "default"}
        />
        <StatusItem
          label="human messages:"
          value={network ? (ingressOn ? "on" : "off") : "—"}
          tone={ingressOn ? "good" : "default"}
        />
        <StatusItem
          label="direct:"
          value={network ? (directMessagesEnabled ? "on" : "off") : "—"}
          tone={directMessagesEnabled ? "good" : "default"}
        />
        <StatusItem value={clock} tone="ink" />
      </div>
    </footer>
  );
}
