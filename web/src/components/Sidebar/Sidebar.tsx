import { useMemo, useState } from "react";
import { useAgents } from "../../hooks/useAgents";
import { ApiError } from "../../lib/api";
import { useNetwork } from "../../hooks/useNetwork";
import { supportsDirectMessages } from "../../lib/capabilities";
import { Panel } from "../Panel";
import type { PanelTab } from "../Panel/PanelTabs";
import { AgentsList } from "./AgentsPanel";
import { DirectChannelsList } from "./DirectChannelsPanel";
import { PairingsList } from "./PairingsPanel";
import { RoomsList } from "./RoomsPanel";

type TabKey = "rooms" | "dms" | "agents" | "pairings";

export function Sidebar() {
  const { data: network } = useNetwork();
  const directMessagesEnabled =
    supportsDirectMessages(network) && (network?.console?.can_read_dms ?? true);
  const pairingsEnabled =
    (network?.capabilities?.pairings ?? true) &&
    (network?.console?.can_read_pairings ?? true);
  const { error: agentsError } = useAgents();
  const agentsEnabled = !(
    agentsError instanceof ApiError && [401, 403].includes(agentsError.status)
  );
  const [active, setActive] = useState<TabKey>("rooms");

  const tabs = useMemo<PanelTab<TabKey>[]>(() => {
    const all: PanelTab<TabKey>[] = [
      { key: "rooms", label: "ROOMS" },
      { key: "dms", label: "DMS" },
      { key: "agents", label: "AGENTS" },
      { key: "pairings", label: "PAIRINGS" },
    ];
    return all.filter(
      (tab) =>
        (tab.key !== "dms" || directMessagesEnabled) &&
        (tab.key !== "pairings" || pairingsEnabled) &&
        (tab.key !== "agents" || agentsEnabled),
    );
  }, [directMessagesEnabled, pairingsEnabled, agentsEnabled]);

  const current = tabs.some((tab) => tab.key === active) ? active : "rooms";

  return (
    <aside className="grid min-h-0 min-w-0">
      <Panel>
        <Panel.Header>
          <Panel.Tabs tabs={tabs} active={current} onSelect={setActive} />
        </Panel.Header>
        <Panel.Body>
          {current === "rooms" ? <RoomsList /> : null}
          {current === "dms" ? <DirectChannelsList /> : null}
          {current === "agents" ? <AgentsList /> : null}
          {current === "pairings" ? <PairingsList /> : null}
        </Panel.Body>
      </Panel>
    </aside>
  );
}
