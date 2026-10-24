import type { Message } from "./types";

export interface Sender {
  /** Stable key used for grouping and colour. */
  key: string;
  /** The id this network can vouch for (see TimelineMessage). */
  id: string;
  /** Self-asserted display name, only when it differs from the id. */
  alias?: string;
  role: string;
  /** Local pairing or follow the message arrived through. */
  via?: string;
  viaKind?: "pairing" | "follow";
  verified: boolean;
}

export function senderOf(message: Message): Sender {
  const via = message.origin?.received_via?.trim();
  const follow = via?.startsWith("follow:") ? via.slice("follow:".length) : undefined;
  const author = follow ? message.origin?.claimed_author ?? message.from : message.from;
  const authorId = author?.id || "unknown";
  const claimedNetwork = author?.network_id?.trim();
  const relayed = follow && claimedNetwork && claimedNetwork !== message.origin?.claimed_discovery_network_id;
  const id = relayed ? `${authorId}@${claimedNetwork}` : authorId;
  const claimedName = author?.name?.trim();
  return {
    // Any change in route, identity, role or trust starts a new header.
    key: [via ?? "", id, message.from?.type ?? "unknown", message.from?.credential_bound === true && !via ? "bound" : ""].join("|"),
    id,
    alias: claimedName && claimedName !== authorId ? claimedName : undefined,
    role: message.from?.type ?? "unknown",
    via: follow ?? via,
    viaKind: via ? (follow ? "follow" : "pairing") : undefined,
    verified: message.from?.credential_bound === true && !via,
  };
}

const AVATAR_COLORS = ["#3ddc84", "#f5c451", "#8bd3ff", "#d97757", "#c4a7ff", "#ff8fb1", "#7ee0d0", "#e8e8e8"];

export function avatarColor(key: string): string {
  let hash = 0;
  for (let i = 0; i < key.length; i++) hash = (hash * 31 + key.charCodeAt(i)) | 0;
  return AVATAR_COLORS[Math.abs(hash) % AVATAR_COLORS.length]!;
}

export function initials(id: string): string {
  const base = id.split(/[:@/]/).filter(Boolean).pop() ?? id;
  const words = base.split(/[-_.\s]+/).filter(Boolean);
  const letters = words.length > 1 ? words[0]![0]! + words[1]![0]! : base.slice(0, 2);
  return letters.toUpperCase();
}

/** The part of an id after its network prefix: "clank-newsroom:brass" → "brass". */
export function shortName(id: string): string {
  const split = id.indexOf(":");
  return split > 0 ? id.slice(split + 1) : id;
}
