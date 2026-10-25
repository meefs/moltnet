import { dayKey } from "../../../lib/format";
import type { Message, MessagePage, RoomHub, RoomHubEvent } from "../../../lib/types";
import { continuesFrom, rowKeyOf } from "./TimelineMessage";

/**
 * React Query stores the newest page first and appends older pages after it.
 * Each API page is already chronological, so only reverse the page groups.
 */
export function messagesOf(pages: MessagePage[] | undefined): Message[] {
  if (!pages) return [];
  return pages
    .slice()
    .reverse()
    .flatMap((page) => page.messages ?? []);
}

export type TimelineRow =
  | { kind: "message"; key: string; created_at: string; message: Message; startsDay: boolean; continued: boolean }
  | { kind: "control"; key: string; created_at: string; event: RoomHubEvent; startsDay: boolean };

/**
 * Interleaves a shared room's membership log with the loaded messages by
 * `created_at`. Only the loaded window is covered: while older pages remain,
 * an event older than the first loaded message is held back, so it appears
 * in place once that page is fetched instead of stacking at the top.
 * Events newer than the last message trail it.
 */
export function buildTimelineRows(messages: Message[], hub: RoomHub | null | undefined, hasOlder: boolean): TimelineRow[] {
  const firstAt = messages.length > 0 ? Date.parse(messages[0]!.created_at) : Number.NaN;
  const events = (hub?.events ?? [])
    .filter((event) => {
      const at = Date.parse(event.created_at);
      if (Number.isNaN(at)) return false;
      return !hasOlder || Number.isNaN(firstAt) || at >= firstAt;
    })
    .slice()
    .sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at) || a.seq - b.seq);

  const rows: TimelineRow[] = [];
  let previousMessage: Message | undefined;
  let previousAt: string | undefined;
  const push = (row: TimelineRow) => {
    rows.push(row);
    previousAt = row.created_at;
  };
  const startsDay = (createdAt: string) => previousAt === undefined || dayKey(previousAt) !== dayKey(createdAt);
  const pushEvent = (event: RoomHubEvent) => {
    push({
      kind: "control",
      key: `hub:${hub?.global_room_id ?? ""}#${event.seq}`,
      created_at: event.created_at,
      event,
      startsDay: startsDay(event.created_at),
    });
    // A system line between two messages breaks the sender run.
    previousMessage = undefined;
  };

  let next = 0;
  for (const message of messages) {
    const at = Date.parse(message.created_at);
    // Events at the same instant as a message come first: the join precedes
    // the joiner's first word.
    while (next < events.length && !(Date.parse(events[next]!.created_at) > at)) pushEvent(events[next++]!);
    push({
      kind: "message",
      key: rowKeyOf(message),
      created_at: message.created_at,
      message,
      startsDay: startsDay(message.created_at),
      continued: continuesFrom(previousMessage, message),
    });
    previousMessage = message;
  }
  while (next < events.length) pushEvent(events[next++]!);
  return rows;
}
