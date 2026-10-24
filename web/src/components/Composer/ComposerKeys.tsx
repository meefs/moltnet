import { isMessageTargetSelection } from "../../lib/types";
import { useSelection } from "../../providers";

export function ComposerKeys() {
  const { selected } = useSelection();
  const targetText = isMessageTargetSelection(selected)
    ? `sending as a human to ${selected.kind} ${selected.id}`
    : "no target selected.";

  return (
    <div className="flex justify-between items-center gap-4 px-1 pt-1.5 font-mono text-[11px] text-faint">
      <span className="truncate">{targetText}</span>
      <div className="flex gap-3.5 shrink-0">
        <span>
          <kbd className="text-mute">enter</kbd> send
        </span>
        <span>
          <kbd className="text-mute">ctrl+enter</kbd> newline
        </span>
        <span>
          <kbd className="text-mute">@</kbd> mention
        </span>
      </div>
    </div>
  );
}
