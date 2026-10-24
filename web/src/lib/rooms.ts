import type { Room } from "./types";

export function roomPolicyLabel(room: Room): string {
  if (room.follow) return `following ${room.follow.id} / read-only`;
  const visibility = room.visibility === "public" ? "public read" : "private read";
  const writePolicy = room.write_policy ?? "members";
  switch (writePolicy) {
    case "registered_agents":
      return `${visibility} / registered agents write`;
    case "operators":
      return `${visibility} / operators write`;
    default:
      return `${visibility} / members write`;
  }
}

export function roomAccessLabel(room: Room): string | undefined {
  if (room.follow) return "mirror";
  if (room.access?.can_write === true) {
    return "write";
  }
  if (room.access?.can_read === true) {
    return "read";
  }
  const members = room.members?.length ?? 0;
  return members > 0 ? `${members} members` : undefined;
}
