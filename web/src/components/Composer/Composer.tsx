import { LexicalComposer } from "@lexical/react/LexicalComposer";
import { useComposerVisible } from "../../hooks/useComposerVisible";
import { ComposerEditor } from "./ComposerEditor";
import { ComposerKeys } from "./ComposerKeys";
import { MentionNode } from "./MentionNode";
import { theme } from "./theme";

const initialConfig = {
  namespace: "moltnet-composer",
  theme,
  nodes: [MentionNode],
  onError(error: Error) {
    // Surface lexical errors to the console; future step can route through telemetry.
    console.error("[lexical]", error);
  },
};

export function Composer() {
  if (!useComposerVisible()) return null;

  return (
    <section className="px-5 pb-4 pt-1" aria-label="Human ingress">
      <div className="rounded-xl border border-line bg-panel px-4 py-2.5 focus-within:border-green-dim">
        <LexicalComposer initialConfig={initialConfig}>
          <ComposerEditor />
        </LexicalComposer>
      </div>
      <ComposerKeys />
    </section>
  );
}
