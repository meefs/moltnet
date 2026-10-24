import { useRooms } from "../../hooks/useRooms";
import { useUnread } from "../../hooks/useUnread";
import { roomPolicyLabel } from "../../lib/rooms";
import { useSelection } from "../../providers";
import { ListItem } from "../ListItem";

export function RoomsList() {
  const { data: rooms = [] } = useRooms();
  const { selected, select } = useSelection();
  const unread = useUnread("room");

  return (
    <>
        {rooms.length === 0 ? (
          <p className="text-faint text-[13px] px-2 py-1">no rooms connected.</p>
        ) : (
          <div className="flex flex-col">
            {rooms.map((room) => {
              const active = selected?.kind === "room" && selected.id === room.id;
              return (
                <ListItem
                  key={room.id}
                  active={active}
                  onClick={() => select({ kind: "room", id: room.id })}
                  title={<><span className="text-faint">#</span> {room.name || room.id}</>}
                  subtitle={room.follow ? roomPolicyLabel(room) : undefined}
                  trailing={room.follow ? "mirror" : <UnreadBadge count={unread.get(room.id)} />}
                />
              );
            })}
          </div>
        )}
      </>
  );
}

export function UnreadBadge({ count }: { count?: number }) {
  if (!count) return undefined;
  return (
    <span className="inline-block min-w-5 rounded-full bg-green px-1.5 text-center text-[11px] font-semibold text-[#03170b]">
      {count > 99 ? "99+" : count}
    </span>
  );
}
