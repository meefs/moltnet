import { useLayoutEffect, useMemo, useRef, useState } from "react";
import { latestReceipt, receiptOf, useEventStream, useSelection } from "../providers";

/**
 * Messages that arrived over the live stream since this tab last showed each
 * channel. Counts only what the stream delivered while the console was open;
 * the server keeps no per-viewer read state. Uses the order this tab received
 * events in, so server/browser clock skew cannot create or hide badges.
 */
export function useUnread(kind: "room" | "dm"): Map<string, number> {
  const { events } = useEventStream();
  const { selected } = useSelection();
  const [openedAt] = useState(latestReceipt);
  const [seen, setSeen] = useState<Record<string, number>>({});
  const current = selected?.kind === kind ? selected.id : null;
  const previous = useRef<string | null>(null);

  // Entering a channel and leaving it both count as reading it up to now.
  // Layout effect, so the channel just left never paints a stale badge.
  useLayoutEffect(() => {
    const left = previous.current;
    previous.current = current;
    if (!current && !left) return;
    const now = latestReceipt();
    setSeen((prev) => ({
      ...prev,
      ...(left ? { [left]: now } : {}),
      ...(current ? { [current]: now } : {}),
    }));
  }, [current]);

  return useMemo(() => {
    const counts = new Map<string, number>();
    for (const event of events) {
      if (event.type !== "message.created" || !event.message) continue;
      const target = event.message.target;
      const id = kind === "room" ? target?.room_id : target?.dm_id;
      if (!id || id === current || target?.kind !== kind) continue;
      if (receiptOf(event) <= (seen[id] ?? openedAt)) continue;
      counts.set(id, (counts.get(id) ?? 0) + 1);
    }
    return counts;
  }, [events, kind, current, seen, openedAt]);
}
