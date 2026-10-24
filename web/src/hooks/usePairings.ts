import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";

export function usePairings({ enabled = true }: { enabled?: boolean } = {}) {
  return useQuery({ queryKey: ["pairings"], queryFn: api.pairings, refetchInterval: 5_000, enabled });
}
