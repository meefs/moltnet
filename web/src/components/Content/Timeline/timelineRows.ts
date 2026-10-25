import { dayKey } from "../../../lib/format";
import type { Message, MessagePage, RoomHub, RoomHubEvent, RoomHubWithheld } from "../../../lib/types";
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
  | { kind: "control"; key: string; created_at: string; event: RoomHubEvent; startsDay: boolean }
  | { kind: "withheld"; key: string; created_at: string; withheld: RoomHubWithheld; startsDay: false };

/**
 * A shared room is shown in room order (the home's `room_seq`), never in
 * arrival order: a message catch-up fills into a gap arrives after its
 * successors. Messages not placed yet (this network's own, still on their
 * way to the home) follow the placed ones in loaded order.
 */
export function inRoomOrder(messages: Message[]): Message[] {
  const placed = messages.filter((message) => typeof message.room_seq === "number");
  if (placed.length === 0) return messages;
  placed.sort((a, b) => a.room_seq! - b.room_seq!);
  return [...placed, ...messages.filter((message) => typeof message.room_seq !== "number")];
}

/** Whether a membership event belongs before a message: by epoch when both
 * carry one (a message placed at epoch E follows the event that opened E),
 * else by time. */
function eventPrecedes(event: RoomHubEvent, message: Message): boolean {
  if (typeof event.epoch === "number" && typeof message.membership_epoch === "number") {
    return event.epoch <= message.membership_epoch;
  }
  return !(Date.parse(event.created_at) > Date.parse(message.created_at));
}

/**
 * Interleaves a shared room's membership log with the loaded messages, in
 * room order. Notices go by membership epoch where messages carry one, else
 * by `created_at`. Only the loaded window is covered: while older pages
 * remain, an event before the first loaded message is held back, so it
 * appears in place once that page is fetched instead of stacking at the
 * top. Events after the last message trail it.
 */
export function buildTimelineRows(loaded: Message[], hub: RoomHub | null | undefined, hasOlder: boolean): TimelineRow[] {
  const messages = hub ? inRoomOrder(loaded) : loaded;
  const first = messages[0];
  const events = (hub?.events ?? [])
    .filter((event) => {
      if (Number.isNaN(Date.parse(event.created_at))) return false;
      return !hasOlder || first === undefined || !eventPrecedes(event, first);
    })
    .slice()
    .sort((a, b) => a.seq - b.seq);
  // A withheld message sits at its room seq, like the message it stands for.
  const firstSeq = first?.room_seq;
  const withheld = (hub?.withheld ?? [])
    .filter((item) => !hasOlder || typeof firstSeq !== "number" || item.room_seq > firstSeq)
    .slice()
    .sort((a, b) => a.room_seq - b.room_seq);

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

  let nextWithheld = 0;
  const pushWithheldBefore = (seq: number) => {
    while (nextWithheld < withheld.length && withheld[nextWithheld]!.room_seq < seq) {
      const item = withheld[nextWithheld++]!;
      rows.push({ kind: "withheld", key: `withheld:${hub?.global_room_id ?? ""}#${item.room_seq}`, created_at: previousAt ?? "", withheld: item, startsDay: false });
      previousMessage = undefined;
    }
  };

  let next = 0;
  for (const message of messages) {
    pushWithheldBefore(typeof message.room_seq === "number" ? message.room_seq : Number.POSITIVE_INFINITY);
    // An event at the same instant (or epoch) as a message comes first: the
    // join precedes the joiner's first word.
    while (next < events.length && eventPrecedes(events[next]!, message)) pushEvent(events[next++]!);
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
  pushWithheldBefore(Number.POSITIVE_INFINITY);
  while (next < events.length) pushEvent(events[next++]!);
  return rows;
}
