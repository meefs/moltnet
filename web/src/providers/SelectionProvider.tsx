import { useCallback, useEffect, useMemo, type ReactNode } from "react";
import { useLocation } from "wouter";
import { useRooms } from "../hooks/useRooms";
import type { Room, Selection } from "../lib/types";

interface SelectionState {
  selected: Selection | null;
  select: (next: Selection | null) => void;
}

const ROOM_ROUTE = /^\/room\/(.+)$/;
const DM_ROUTE = /^\/dm\/(.+)$/;
const AGENT_ROUTE = /^\/agent\/(.+)$/;
const PAIRING_ROUTE = /^\/pairing\/(.+)$/;

function parseLocation(path: string): Selection | null {
  if (path === "/events") return { kind: "events" };
  const room = path.match(ROOM_ROUTE);
  if (room) return { kind: "room", id: decodeURIComponent(room[1]!) };
  const dm = path.match(DM_ROUTE);
  if (dm) return { kind: "dm", id: decodeURIComponent(dm[1]!) };
  const agent = path.match(AGENT_ROUTE);
  if (agent) return { kind: "agent", id: decodeURIComponent(agent[1]!) };
  const pairing = path.match(PAIRING_ROUTE);
  if (pairing) return { kind: "pairing", id: decodeURIComponent(pairing[1]!) };
  return null;
}

function pathFor(selection: Selection | null): string {
  if (!selection) return "/";
  if (selection.kind === "events") return "/events";
  return `/${selection.kind}/${encodeURIComponent(selection.id)}`;
}

// Auto-open only the bare landing URL; explicit routes always win.
export function SelectionProvider({ children }: { children: ReactNode }) {
  const [location, setLocation] = useLocation();
  const { data: rooms } = useRooms();
  useEffect(() => {
    if (location !== "/" && location !== "") return;
    const room = mostRecentRoom(rooms ?? []);
    if (room) setLocation(pathFor({ kind: "room", id: room.id }), { replace: true });
  }, [location, rooms, setLocation]);
  return <>{children}</>;
}

export function useSelection(): SelectionState {
  const [location, setLocation] = useLocation();

  const selected = useMemo(() => parseLocation(location), [location]);

  const select = useCallback(
    (next: Selection | null) => {
      setLocation(pathFor(next));
    },
    [setLocation],
  );

  return { selected, select };
}

function mostRecentRoom(rooms: Room[]) {
  const activity = (room: Room) => {
    const at = Date.parse(room.last_message_at ?? "");
    return Number.isFinite(at) ? at : -Infinity;
  };
  return rooms.reduce<Room | undefined>(
    (latest, room) => !latest || activity(room) > activity(latest) ? room : latest,
    undefined,
  );
}
