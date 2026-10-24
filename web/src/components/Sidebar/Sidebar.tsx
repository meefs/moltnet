import { useAgents } from "../../hooks/useAgents";
import { useRooms } from "../../hooks/useRooms";
import { useDMs } from "../../hooks/useDMs";
import { usePairings } from "../../hooks/usePairings";
import { ApiError } from "../../lib/api";
import { useNetwork } from "../../hooks/useNetwork";
import { supportsDirectMessages } from "../../lib/capabilities";
import { useEventStream, useSelection } from "../../providers";
import { AgentsList } from "./AgentsPanel";
import { DirectChannelsList } from "./DirectChannelsPanel";
import { PairingsList } from "./PairingsPanel";
import { RailSection } from "./RailSection";
import { RoomsList } from "./RoomsPanel";

/** The left rail: every place a message can live, in one scrollable column. */
export function Sidebar() {
  const { data: network } = useNetwork();
  const directMessagesEnabled =
    supportsDirectMessages(network) && (network?.console?.can_read_dms ?? true);
  const pairingsEnabled =
    (network?.capabilities?.pairings ?? true) &&
    (network?.console?.can_read_pairings ?? true);
  const { data: agents = [], error: agentsError } = useAgents();
  const agentsEnabled = !(
    agentsError instanceof ApiError && [401, 403].includes(agentsError.status)
  );
  const { data: rooms = [] } = useRooms();
  const { data: dms = [] } = useDMs();
  const { data: pairings } = usePairings();
  const { events } = useEventStream();
  const { selected, select } = useSelection();
  const eventsActive = selected?.kind === "events";

  return (
    <aside className="flex flex-col min-h-0 min-w-0 border-r border-border bg-bg">
      <nav className="flex-1 overflow-auto px-2 pt-3" aria-label="Channels">
        <RailSection title="Rooms" count={rooms.length}>
          <RoomsList />
        </RailSection>
        {directMessagesEnabled ? (
          <RailSection title="Direct" count={dms.length}>
            <DirectChannelsList />
          </RailSection>
        ) : null}
        {pairingsEnabled ? (
          <RailSection title="Paired networks" count={pairings?.length}>
            <PairingsList />
          </RailSection>
        ) : null}
        {agentsEnabled ? (
          <RailSection title="Agents" count={agents.length} collapsible defaultOpen={false}>
            <AgentsList />
          </RailSection>
        ) : null}
      </nav>
      <div className="border-t border-border p-2">
        <button
          type="button"
          onClick={() => select({ kind: "events" })}
          aria-pressed={eventsActive}
          className={`w-full flex items-center justify-between rounded-md px-2 py-1.5 font-mono text-[12px] cursor-pointer transition-colors ${
            eventsActive ? "bg-panel-2 text-green" : "text-mute hover:bg-panel-2/60 hover:text-ink"
          }`}
        >
          <span>event log</span>
          <span className="text-faint">{events.length}</span>
        </button>
      </div>
    </aside>
  );
}
