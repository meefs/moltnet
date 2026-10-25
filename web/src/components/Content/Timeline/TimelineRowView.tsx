import { controlEventText } from "../../../lib/trust";
import type { RoomHub } from "../../../lib/types";
import type { TimelineRow } from "./timelineRows";
import { TimelineMessage } from "./TimelineMessage";
import { ControlEventLine, DayDivider } from "./TimelinePlaceholders";

/** One virtualized row: a day divider when a day starts, then the row body. */
export function TimelineRowView({ row, hub }: { row: TimelineRow; hub: RoomHub | null | undefined }) {
  return (
    <>
      {row.startsDay ? <DayDivider date={row.created_at} /> : null}
      {row.kind === "message" ? (
        <TimelineMessage message={row.message} continued={row.continued} />
      ) : hub ? (
        <ControlEventLine text={controlEventText(row.event, hub)} date={row.created_at} />
      ) : null}
    </>
  );
}
