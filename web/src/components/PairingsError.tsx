import { ApiError } from "../lib/api";

export function PairingsError({ error }: { error: unknown }) {
  const forbidden = error instanceof ApiError && [401, 403].includes(error.status);
  return (
    <p role="alert" className="text-coral text-xs px-2 py-1.5">
      {forbidden ? "pairings access forbidden." : "pairings unavailable."}
    </p>
  );
}
