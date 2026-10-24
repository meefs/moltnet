import { createContext, useCallback, useContext, useRef, useState, useSyncExternalStore, type ReactNode } from "react";

interface RowStore {
  get: (key: string) => boolean;
  set: (key: string, value: boolean) => void;
  subscribe: (key: string, listener: () => void) => () => void;
}

const RowStateContext = createContext<RowStore | null>(null);

function createRowStore(): RowStore {
  const values = new Map<string, boolean>();
  const listeners = new Map<string, Set<() => void>>();
  return {
    get: (key) => values.get(key) ?? false,
    set: (key, value) => {
      if ((values.get(key) ?? false) === value) return;
      values.set(key, value);
      listeners.get(key)?.forEach((listener) => listener());
    },
    subscribe: (key, listener) => {
      const set = listeners.get(key) ?? new Set();
      set.add(listener);
      listeners.set(key, set);
      return () => set.delete(listener);
    },
  };
}

/**
 * Per-message UI choices (an image the viewer agreed to load, a card switched
 * to raw JSON). Rows are virtualized and unmount when scrolled away, so their
 * own state would reset; this keeps it for as long as the console is open.
 * Each flag notifies only the row reading it.
 */
export function RowStateProvider({ children }: { children: ReactNode }) {
  const store = useRef<RowStore | null>(null);
  store.current ??= createRowStore();
  return <RowStateContext.Provider value={store.current}>{children}</RowStateContext.Provider>;
}

const noopSubscribe = () => () => {};

/** A remembered boolean for one message part; local state outside a timeline. */
export function useRowFlag(key: string | undefined): [boolean, (value: boolean) => void] {
  const shared = useContext(RowStateContext);
  const [local, setLocal] = useState(false);
  const active = shared && key ? shared : null;
  const subscribe = useCallback(
    (listener: () => void) => (active && key ? active.subscribe(key, listener) : noopSubscribe()),
    [active, key],
  );
  const value = useSyncExternalStore(subscribe, () => (active && key ? active.get(key) : false));
  const set = useCallback((next: boolean) => (active && key ? active.set(key, next) : setLocal(next)), [active, key]);
  return active ? [value, set] : [local, set];
}
