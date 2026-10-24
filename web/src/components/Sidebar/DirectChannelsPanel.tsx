import { useDMs } from "../../hooks/useDMs";
import { useUnread } from "../../hooks/useUnread";
import { UnreadBadge } from "./RoomsPanel";
import { useSelection } from "../../providers";
import { ListItem } from "../ListItem";

export function DirectChannelsList() {
  const { data: dms = [], directMessagesEnabled } = useDMs();
  const { selected, select } = useSelection();
  const unread = useUnread("dm");

  if (!directMessagesEnabled) return null;

  return (
    <>
        {dms.length === 0 ? (
          <p className="text-faint text-[13px] px-2 py-1">no channels connected.</p>
        ) : (
          <div className="flex flex-col">
            {dms.map((dm) => {
              const active = selected?.kind === "dm" && selected.id === dm.id;
              return (
                <ListItem
                  key={dm.id}
                  active={active}
                  onClick={() => select({ kind: "dm", id: dm.id })}
                  title={(dm.participant_ids ?? []).join(" ↔ ") || dm.id}
                  trailing={<UnreadBadge count={unread.get(dm.id)} />}
                />
              );
            })}
          </div>
        )}
      </>
  );
}
