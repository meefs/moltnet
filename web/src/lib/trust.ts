import { shortFingerprint } from "./format";
import type { MessageOrigin, RoomHub, RoomHubEvent, RoomHubMember } from "./types";

export type TrustTone = "green" | "amber" | "muted" | "coral";

export interface TrustBadgeSpec {
  label: string;
  title: string;
  tone: TrustTone;
}

function keySuffix(fingerprint: string | undefined): string {
  const short = shortFingerprint(fingerprint);
  return short ? ` (${short})` : "";
}

/**
 * The per-message signature verdict. `unsigned` and absent render nothing: an
 * unsigned message is the ordinary case on a local or paired room.
 */
export function signatureBadge(origin: MessageOrigin | undefined): TrustBadgeSpec | null {
  const key = keySuffix(origin?.fingerprint);
  switch (origin?.signature) {
    case "verified":
      return { label: "✓", tone: "green", title: `Signature verified: key pinned independently${key}` };
    case "introduced": {
      const by = origin.introduced_by?.trim() || "the room home";
      return {
        label: `introduced by ${by}`,
        tone: "amber",
        title: `Signature valid, but the key was vouched for only by the room home ${by}; identity not independently verified${key}`,
      };
    }
    case "unconfirmed":
      return { label: "unconfirmed", tone: "muted", title: `Signature valid against a key first seen on this pairing; not yet confirmed${key}` };
    case "invalid":
      return { label: "invalid signature", tone: "coral", title: `Signature does not verify against the sender's key${key}` };
    default:
      return null;
  }
}

/** The trust this network holds in a shared-room member network's key. */
export function memberTrustBadge(member: RoomHubMember, hub: RoomHub): TrustBadgeSpec | null {
  const key = keySuffix(member.fingerprint);
  switch (member.trust) {
    case "self":
      return { label: "this network", tone: "muted", title: `This network's own key${key}` };
    case "verified":
      return { label: "✓", tone: "green", title: `Key pinned independently${key}` };
    case "introduced":
      return {
        label: `introduced by ${hub.home_network_id}`,
        tone: "amber",
        title: `Key vouched for only by the room home ${hub.home_network_id}; identity not independently verified${key}`,
      };
    case "unconfirmed":
      return { label: "unconfirmed", tone: "muted", title: `Key first seen on this pairing; not yet confirmed${key}` };
    case "conflict":
      return {
        label: "key conflict",
        tone: "coral",
        title: `The room names a key${key} other than the one this network pinned for ${member.network_id}; its messages are refused`,
      };
    default:
      return null;
  }
}

/** "shared · home: newsroom", with the global id and history policy as title. */
export function sharedRoomChip(hub: RoomHub): { label: string; title: string } {
  const home = hub.is_home ? "this network" : hub.home_network_id;
  const history = hub.history ? `history: ${hub.history}` : "history policy not reported";
  return { label: `shared · home: ${home}`, title: `${hub.global_room_id}\n${history}` };
}

/** One muted system line per membership-log entry. */
export function controlEventText(event: RoomHubEvent, hub: RoomHub): string {
  const network = event.network_id || "a network";
  switch (event.kind) {
    case "genesis":
      return `shared room created by ${network}`;
    case "add":
      return event.role ? `${network} joined (${event.role})` : `${network} joined`;
    case "remove":
      return `${network} was removed`;
    case "leave":
      return `${network} left`;
    case "set_role":
      return `${network} is now ${event.role || "a member"}`;
    case "set_policy":
      return `history policy set to ${event.history || hub.history || "unknown"}`;
    default:
      return `${network}: ${event.kind}`;
  }
}

/** A "received …" note when receipt lags the signed creation time by > 5s. */
export function receivedLag(createdAt: string, receivedAt: string | undefined): Date | null {
  if (!receivedAt) return null;
  const received = new Date(receivedAt);
  const created = Date.parse(createdAt);
  if (Number.isNaN(received.valueOf()) || Number.isNaN(created)) return null;
  return Math.abs(received.valueOf() - created) > 5_000 ? received : null;
}
