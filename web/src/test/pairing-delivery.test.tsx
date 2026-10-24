import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Pairing } from "../lib/types";
import { PairingView } from "../components/Content/PairingView/PairingView";

let pairing: Pairing;
vi.mock("../providers", () => ({ useSelection: () => ({ selected: { kind: "pairing", id: "peer" } }) }));
vi.mock("../hooks/usePairings", () => ({ usePairings: () => ({ data: [pairing] }) }));
vi.mock("../hooks/usePairingNetwork", () => ({ usePairingNetwork: () => ({}) }));
vi.mock("../hooks/usePairingRooms", () => ({ usePairingRooms: () => ({}) }));
vi.mock("../hooks/usePairingAgents", () => ({ usePairingAgents: () => ({}) }));

beforeEach(() => {
  pairing = { id: "peer", remote_network_id: "remote", status: "connected" };
});

describe("pairing delivery status", () => {
  it("keeps older servers without delivery metadata usable", () => {
    render(<PairingView />);
    expect(screen.getByText(/PAIRING: peer/)).toBeTruthy();
    expect(screen.queryByRole("region", { name: "Outbound delivery" })).toBeNull();
  });

  it("shows pending, age, drops and the last delivery error", () => {
    pairing.delivery = { pending: 12, oldest_pending_age_seconds: 7_200, dropped: 5, last_error: "relay is not connected" };
    render(<PairingView />);
    const section = screen.getByRole("region", { name: "Outbound delivery" });
    expect(section.textContent).toContain("pending messages :12");
    expect(section.textContent).toContain("oldest pending :2h");
    expect(section.textContent).toContain("dropped messages :5");
    expect(section.textContent).toContain("last delivery error :relay is not connected");
  });

  it("keeps cumulative drops and rejections visible after the queue drains", () => {
    pairing.delivery = { pending: 0, oldest_pending_age_seconds: 0, dropped: 5, rejected: 2, last_rejection_reason: "paired network returned HTTP 403 Forbidden" };
    render(<PairingView />);
    const section = screen.getByRole("region", { name: "Outbound delivery" });
    expect(section.textContent).toContain("pending messages :0");
    expect(section.textContent).toContain("oldest pending :—");
    expect(section.textContent).toContain("dropped messages :5");
    expect(section.textContent).not.toContain("last delivery error :");
    expect(section.textContent).toContain("rejected messages :2");
    expect(section.textContent).toContain("last rejection reason :paired network returned HTTP 403 Forbidden");
  });
});
