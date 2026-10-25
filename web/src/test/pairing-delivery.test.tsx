import { render, screen, within } from "@testing-library/react";
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
    expect(screen.getByRole("heading", { name: "paired with peer" })).toBeTruthy();
    expect(screen.queryByRole("region", { name: "Outbound delivery" })).toBeNull();
  });

  it("shows pending, age, drops and the last delivery error", () => {
    pairing.delivery = { pending: 12, oldest_pending_age_seconds: 7_200, dropped: 5, last_error: "relay is not connected" };
    render(<PairingView />);
    const section = screen.getByRole("region", { name: "Outbound delivery" });
    expect(within(section).getByRole("group", { name: "pending messages" }).textContent).toBe("12pending messages");
    expect(within(section).getByRole("group", { name: "oldest pending" }).textContent).toBe("2holdest pending");
    expect(within(section).getByRole("group", { name: "dropped messages" }).textContent).toBe("5dropped messages");
    expect(within(section).getByText("relay is not connected")).toBeTruthy();
  });

  it("keeps cumulative drops and rejections visible after the queue drains", () => {
    pairing.delivery = { pending: 0, oldest_pending_age_seconds: 0, dropped: 5, rejected: 2, last_rejection_reason: "paired network returned HTTP 403 Forbidden" };
    render(<PairingView />);
    const section = screen.getByRole("region", { name: "Outbound delivery" });
    expect(within(section).getByRole("group", { name: "pending messages" }).textContent).toBe("0pending messages");
    expect(within(section).getByRole("group", { name: "oldest pending" }).textContent).toBe("-oldest pending");
    expect(within(section).getByRole("group", { name: "dropped messages" }).textContent).toBe("5dropped messages");
    expect(section.textContent).not.toContain("last delivery error");
    expect(within(section).getByRole("group", { name: "rejected messages" }).textContent).toBe("2rejected messages");
    expect(within(section).getByText("paired network returned HTTP 403 Forbidden")).toBeTruthy();
  });
});
