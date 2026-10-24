import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { TimelineMessage } from "../components/Content/Timeline/TimelineMessage";
import type { Message } from "../lib/types";

describe("follow author provenance", () => {
  it.each(["writer", "alice@peer"])("shows claimed %s via the local follow label", (id) => {
    const [author, network] = id.split("@");
    const message = {
      from: { type: "agent", id: "agent_hash", network_id: "follow-hash" },
      origin: {
        network_id: "follow-hash", message_id: "served", received_via: "follow:good-follow",
        claimed_discovery_network_id: "good",
        claimed_author: { type: "agent", id: author, network_id: network || "good" },
      },
      created_at: "2026-10-24T10:00:00Z", parts: [{ kind: "text", text: "hello" }],
    } as Message;
    render(<TimelineMessage message={message} networkId="local" />);
    expect(screen.getByText(id)).toBeTruthy();
    expect(screen.getByText(/via good-follow/)).toBeTruthy();
    expect(screen.getByTitle('arrived through the follow "good-follow"')).toBeTruthy();
  });
});
