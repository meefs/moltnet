import { useMemo } from "react";
import { useAgents } from "../../../hooks/useAgents";
import { useDMs } from "../../../hooks/useDMs";
import { useRooms } from "../../../hooks/useRooms";
import { useSelection } from "../../../providers";
import { DetailRow } from "../../DetailRow";
import { ListItem } from "../../ListItem";
import { Panel } from "../../Panel";
import { SectionLabel } from "../../SectionLabel";
import { avatarColor, initials, shortName } from "../../../lib/sender";

export function AgentView() {
  const { selected, select } = useSelection();
  const { data: agents = [] } = useAgents();
  const { data: rooms = [] } = useRooms();
  const { data: dms = [], directMessagesEnabled } = useDMs();

  const agentId = selected?.kind === "agent" ? selected.id : null;
  const agent = useMemo(
    () => (agentId ? agents.find((a) => a.id === agentId) : null),
    [agents, agentId],
  );

  const memberRooms = useMemo(() => {
    if (!agentId) return [];
    return rooms.filter(
      (room) =>
        (room.members ?? []).includes(agentId) ||
        (agent?.rooms ?? []).includes(room.id),
    );
  }, [rooms, agent, agentId]);

  const participatingDMs = useMemo(() => {
    if (!agentId) return [];
    return dms.filter((dm) => (dm.participant_ids ?? []).includes(agentId));
  }, [dms, agentId]);

  if (!agent || !agentId) {
    return (
      <Panel>
        <Panel.Header>
          <Panel.Title>AGENT</Panel.Title>
        </Panel.Header>
        <Panel.Body>
          <p className="text-faint text-[13px] px-2 py-1.5">
            agent not found{agentId ? ` — ${agentId}` : ""}.
          </p>
        </Panel.Body>
      </Panel>
    );
  }

  return (
    <Panel>
      <Panel.Body>
        <div className="flex items-center gap-4 mb-5">
          <span
            aria-hidden="true"
            className="grid place-items-center w-14 h-14 rounded-xl text-[20px] font-bold text-[#111]"
            style={{ background: avatarColor(shortName(agent.id)) }}
          >
            {initials(agent.id)}
          </span>
          <div className="min-w-0">
            <h1 className="text-[22px] font-semibold tracking-[-0.02em] truncate">{shortName(agent.id)}</h1>
            <p className="font-mono text-[12px] text-mute">
              <span className={agent.connected ? "text-green" : "text-faint"}>
                {agent.connected ? "● connected" : "○ not connected"}
              </span>
              {" · "}
              {agent.network_id ?? "unknown network"}
            </p>
          </div>
        </div>
        <div className="max-w-2xl">
          <DetailRow label="agent" value={agent.id} />
          <DetailRow
            label="canonical id"
            value={agent.fqid ?? agent.id}
          />
          <DetailRow label="network" value={agent.network_id ?? "unknown"} />
        </div>

        <SectionLabel title="Rooms" count={memberRooms.length} />
        {memberRooms.length === 0 ? (
          <p className="text-faint text-[13px] px-2 py-1 mb-4">
            no rooms — this agent is not a member of any room.
          </p>
        ) : (
          <div className="flex flex-col max-w-2xl mb-4">
            {memberRooms.map((room) => (
              <ListItem
                key={room.id}
                showMarker={false}
                onClick={() => select({ kind: "room", id: room.id })}
                title={<><span className="text-faint">#</span> {room.name || room.id}</>}
                trailing={
                  (room.members ?? []).length > 0
                    ? `${room.members!.length} members`
                    : undefined
                }
              />
            ))}
          </div>
        )}

        {directMessagesEnabled ? (
          <>
            <SectionLabel title="Direct channels" count={participatingDMs.length} />
            {participatingDMs.length === 0 ? (
              <p className="text-faint text-[13px] px-2 py-1">
                no direct channels — this agent has no DMs.
              </p>
            ) : (
              <div className="flex flex-col">
                {participatingDMs.map((dm) => (
                  <ListItem
                    key={dm.id}
                    showMarker={false}
                    onClick={() => select({ kind: "dm", id: dm.id })}
                    title={dm.id}
                    trailing={`${dm.message_count ?? 0} msgs`}
                  />
                ))}
              </div>
            )}
          </>
        ) : null}
      </Panel.Body>
    </Panel>
  );
}

