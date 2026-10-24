import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";

interface RowState {
  get: (key: string) => boolean;
  set: (key: string, value: boolean) => void;
}

const RowStateContext = createContext<RowState | null>(null);

/**
 * Per-message UI choices (an image the viewer agreed to load, a card switched
 * to raw JSON). Rows are virtualized and unmount when scrolled away, so their
 * own state would reset; this keeps it for as long as the timeline is open.
 */
export function RowStateProvider({ children }: { children: ReactNode }) {
  const store = useRef(new Map<string, boolean>());
  const [version, setVersion] = useState(0);
  const set = useCallback((key: string, value: boolean) => {
    store.current.set(key, value);
    setVersion((current) => current + 1);
  }, []);
  const value = useMemo<RowState>(
    () => ({ get: (key) => store.current.get(key) ?? false, set }),
    // A new object per change, so rows reading a flag re-render.
    [version, set],
  );
  return <RowStateContext.Provider value={value}>{children}</RowStateContext.Provider>;
}

/** A remembered boolean for one message part; local state outside a timeline. */
export function useRowFlag(key: string | undefined): [boolean, (value: boolean) => void] {
  const shared = useContext(RowStateContext);
  const [local, setLocal] = useState(false);
  if (!shared || !key) return [local, setLocal];
  return [shared.get(key), (value) => shared.set(key, value)];
}
