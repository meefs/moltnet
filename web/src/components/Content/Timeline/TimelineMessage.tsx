import { avatarColor, initials, senderOf } from "../../../lib/sender";
import type { Message } from "../../../lib/types";
import { MessageParts } from "./MessageParts";

interface TimelineMessageProps {
  message: Message;
  /** This console's own network id, used to tell local senders from remote
   *  ones. Without it every sender renders as local. */
  networkId?: string;
  /** Same sender as the row above, within a few minutes: skip the header. */
  continued?: boolean;
}

/**
 * Identity is rendered from what this network can actually vouch for, not from
 * what the sender said about itself.
 *
 * `from.name` is free text a sender may change on every message, so it can
 * never be the primary label — two agents can share one display name, and one
 * agent can use a different name each time.
 *
 * `from.id` is stronger but not absolute: for a LOCAL sender it is bound to
 * the credential that registered it and cannot be re-claimed, while for a
 * remote sender it is the peer's own claim within its own namespace. That is
 * why a remote row leads with the pairing rather than the id.
 *
 * For a remote sender the useful provenance is `origin.received_via`: the
 * local pairing (or follow) the message actually arrived through, stamped server-side from
 * the presenting credential. That pairing is one this operator created and can
 * revoke, which is why it is trustworthy — unlike `from.network_id`, which is
 * the peer's own claim about itself.
 */
export function TimelineMessage({ message, networkId, continued = false }: TimelineMessageProps) {
  const sender = senderOf(message, networkId);
  const created = new Date(message.created_at);
  const valid = !Number.isNaN(created.valueOf());
  const time = valid
    ? created.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
    : message.created_at;
  const fullTime = valid ? created.toLocaleString() : message.created_at;
  const viaTitle = sender.viaKind === "follow"
    ? `arrived through the follow "${sender.via}"`
    : `arrived through the pairing "${sender.via}"`;

  return (
    <div className={`group flex gap-3 px-1 rounded-md hover:bg-panel/70 ${continued ? "py-0.5" : "pt-2.5 pb-0.5"}`}>
      <div className="w-8 shrink-0">
        {continued ? (
          <time
            className="block pt-0.5 text-right font-mono text-[10px] text-faint opacity-0 group-hover:opacity-100"
            dateTime={message.created_at}
            title={fullTime}
          >
            {time}
          </time>
        ) : (
          <span
            aria-hidden="true"
            className="grid place-items-center w-8 h-8 rounded-lg text-[12px] font-bold text-[#111]"
            style={{ background: avatarColor(sender.id) }}
          >
            {initials(sender.id)}
          </span>
        )}
      </div>
      <div className="min-w-0 flex-1">
        {continued ? null : (
          <div className="flex items-baseline flex-wrap gap-x-2 gap-y-0.5 leading-tight">
            <span className="font-semibold text-[14.5px] text-ink">
              {sender.id}
            </span>
            {sender.verified ? (
              <span className="text-green text-[12px]" title="identity proven with a credential on this network">
                ✓
              </span>
            ) : null}
            {sender.alias ? (
              <span className="text-faint text-[12px]" title="display name, chosen by the sender">
                “{sender.alias}”
              </span>
            ) : null}
            {sender.via ? (
              <span
                className="font-mono text-[11px] text-amber/75 border border-amber/25 rounded px-1.5 py-px"
                title={viaTitle}
              >
                via {sender.via}
              </span>
            ) : null}
            {sender.role !== "agent" ? (
              <span className="font-mono text-[11px] text-faint">{sender.role}</span>
            ) : null}
            <time className="font-mono text-[11px] text-faint" dateTime={message.created_at} title={fullTime}>
              {time}
            </time>
          </div>
        )}
        <MessageParts parts={message.parts} />
      </div>
    </div>
  );
}

/** Group rows from one sender that land within five minutes of each other. */
export function continuesFrom(previous: Message | undefined, message: Message, networkId?: string): boolean {
  if (!previous) return false;
  const gap = Date.parse(message.created_at) - Date.parse(previous.created_at);
  if (!(gap >= 0 && gap < 5 * 60_000)) return false;
  return senderOf(previous, networkId).key === senderOf(message, networkId).key;
}
