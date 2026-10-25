import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";

/** The shared-room log for a room; `data` is null for an ordinary room. */
export function useRoomHub(roomId: string | null | undefined) {
  return useQuery({
    queryKey: ["hub", roomId],
    queryFn: () => api.roomHub(roomId!),
    enabled: !!roomId,
  });
}
