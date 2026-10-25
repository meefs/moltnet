import { useMemo } from "react";
import { useAgents } from "../../hooks/useAgents";
import { useNetwork } from "../../hooks/useNetwork";
import { useRoomHub } from "../../hooks/useRoomHub";
import { useRooms } from "../../hooks/useRooms";
import { avatarColor, initials } from "../../lib/sender";
import { memberTrustBadge } from "../../lib/trust";
import type { RoomHubMember } from "../../lib/types";
import { useSelection } from "../../providers";
import { TrustBadge } from "../TrustBadge";

interface MemberGroup {
  home: string;
  members: { id: string; name: string; connected?: boolean }[];
  /** The network's entry in a shared room's roster. */
  roster?: RoomHubMember;
}

/**
 * Who is in the open room, grouped by the network each member belongs to. In
 * a shared room the hub roster adds every member network, with its role and
 * the trust this network holds in its key, even when none of its agents are
 * known here.
 */
export function MembersPanel() {
  const { selected, select } = useSelection();
  const { data: rooms = [] } = useRooms();
  const { data: agents = [] } = useAgents();
  const { data: network } = useNetwork();
  const room = selected?.kind === "room" ? rooms.find((item) => item.id === selected.id) : undefined;
  const { data: hub } = useRoomHub(room?.id);

  const groups = useMemo<MemberGroup[]>(() => {
    const byNetwork = new Map<string, { id: string; name: string; connected?: boolean }[]>();
    for (const member of room?.members ?? []) {
      const agent = agents.find((item) => item.id === member);
      const split = member.indexOf(":");
      const home = agent?.network_id || (split > 0 ? member.slice(0, split) : network?.id || "this network");
      const name = split > 0 ? member.slice(split + 1) : member;
      const list = byNetwork.get(home) ?? [];
      list.push({ id: member, name, connected: agent?.connected });
      byNetwork.set(home, list);
    }
    const roster = new Map<string, RoomHubMember>();
    for (const member of hub?.members ?? []) {
      // The roster's own entry may name this network by an id the room
      // members do not use; "self" files it with the local members.
      const home = member.trust === "self" && network?.id ? network.id : member.network_id;
      roster.set(home, member);
      if (!byNetwork.has(home)) byNetwork.set(home, []);
    }
    return [...byNetwork.entries()]
      .map(([home, members]) => ({
        home,
        members: members.sort((a, b) => a.name.localeCompare(b.name)),
        roster: roster.get(home),
      }))
      .sort((a, b) => (a.home === network?.id ? -1 : b.home === network?.id ? 1 : a.home.localeCompare(b.home)));
  }, [room, agents, hub, network?.id]);

  if (!room || groups.length === 0) return null;

  return (
    <aside className="hidden xl:flex flex-col w-64 min-h-0 border-l border-border overflow-auto px-3 py-4" aria-label="Room members">
      {groups.map(({ home, members, roster }) => (
        <section key={home} className="mb-5" aria-label={`network ${home}`}>
          <h2 className="flex items-center gap-2 px-2 mb-1.5 font-mono text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">
            <span className="w-2 h-2 rounded-full shrink-0" style={{ background: avatarColor(home) }} aria-hidden="true" />
            <span className="truncate">{home}</span>
            {members.length > 0 ? <span className="text-faint font-normal">{members.length}</span> : null}
          </h2>
          {roster && hub ? (
            <p className="flex flex-wrap items-center gap-1.5 px-2 mb-1.5 pl-6">
              {roster.role ? <span className="font-mono text-[11px] text-faint">{roster.role}</span> : null}
              {roster.network_id === hub.home_network_id ? (
                <span className="font-mono text-[11px] text-faint">· home</span>
              ) : null}
              <TrustBadge spec={memberTrustBadge(roster, hub)} />
            </p>
          ) : null}
          {members.map((member) => (
            <button
              key={member.id}
              type="button"
              onClick={() => select({ kind: "agent", id: member.id })}
              className="w-full flex items-center gap-2.5 rounded-md px-2 py-1.5 text-left text-[14px] text-sub hover:bg-panel-2/60 hover:text-ink cursor-pointer"
            >
              <span className="relative shrink-0">
                <span
                  aria-hidden="true"
                  className="grid place-items-center w-6 h-6 rounded-md text-[10px] font-bold text-[#111]"
                  style={{ background: avatarColor(member.name) }}
                >
                  {initials(member.name)}
                </span>
                {member.connected !== undefined ? (
                  <span
                    className={`absolute -right-0.5 -bottom-0.5 w-2.5 h-2.5 rounded-full border-2 border-bg ${member.connected ? "bg-green" : "bg-faint"}`}
                    title={member.connected ? "connected now" : "not connected"}
                  />
                ) : null}
              </span>
              <span className="truncate">{member.name}</span>
            </button>
          ))}
        </section>
      ))}
    </aside>
  );
}
