import { useDMs } from "../../../hooks/useDMs";
import { useRooms } from "../../../hooks/useRooms";
import { roomPolicyLabel } from "../../../lib/rooms";
import { sharedRoomChip } from "../../../lib/trust";
import type { MessageTargetSelection, RoomHub } from "../../../lib/types";

interface TimelineHeaderProps {
  selection: MessageTargetSelection;
  loaded: number;
  hasOlder: boolean;
  /** The room's shared-room log; null or absent for an ordinary room. */
  hub?: RoomHub | null;
}

export function TimelineHeader({ selection, loaded, hasOlder, hub }: TimelineHeaderProps) {
  const { data: rooms = [] } = useRooms();
  const { data: dms = [] } = useDMs();
  const room = selection.kind === "room" ? rooms.find((item) => item.id === selection.id) : undefined;
  const dm = selection.kind === "dm" ? dms.find((item) => item.id === selection.id) : undefined;

  const chips: string[] = [];
  if (room) {
    chips.push(roomPolicyLabel(room).replace(" / ", " · "));
    if (room.access?.can_write) chips.push("you can write");
    else if (room.access?.can_read) chips.push("you can read");
  }

  const shared = selection.kind === "room" && hub ? sharedRoomChip(hub) : null;
  const count = `${loaded}${hasOlder ? "+" : ""} messages`;
  const members = room?.members?.length;

  return (
    <header className="flex items-center gap-3 h-14 shrink-0 px-5 border-b border-border min-w-0">
      <h1 className="text-[17px] font-semibold tracking-[-0.01em] truncate">
        {selection.kind === "room" ? (
          <>
            <span className="text-faint font-normal">#</span> {room?.name || selection.id}
          </>
        ) : (
          (dm?.participant_ids ?? []).join(" ↔ ") || selection.id
        )}
      </h1>
      {shared ? (
        <span className="shrink-0 rounded-md border border-green/30 px-2 py-0.5 font-mono text-[11px] text-green" title={shared.title}>
          {shared.label}
        </span>
      ) : null}
      {chips.map((chip) => (
        <span key={chip} className="shrink-0 rounded-md border border-line px-2 py-0.5 font-mono text-[11px] text-mute">
          {chip}
        </span>
      ))}
      <span className="flex-1" />
      <span className="shrink-0 font-mono text-[11px] text-faint">
        {members ? `${members} members · ` : ""}
        {count}
      </span>
    </header>
  );
}
