// The store reaches the components through a context, so a test renders
// its own store over its own mock API, with no global state.
import { createContext, type ReactNode, useContext } from "react";
import { useStore } from "zustand";
import type { StoreApi } from "zustand/vanilla";
import type { ReadStore } from "../store.js";

const ReadContext = createContext<StoreApi<ReadStore> | null>(null);

/** StoreProvider gives its children one store. */
export function StoreProvider(props: { store: StoreApi<ReadStore>; children: ReactNode }) {
  return <ReadContext.Provider value={props.store}>{props.children}</ReadContext.Provider>;
}

/** useReadStore is the store itself, for its actions outside a render. */
export function useReadStore(): StoreApi<ReadStore> {
  const store = useContext(ReadContext);
  if (store === null) throw new Error("read-web: a component outside StoreProvider");
  return store;
}

/** useRead subscribes a component to part of the store. */
export function useRead<T>(selector: (state: ReadStore) => T): T {
  return useStore(useReadStore(), selector);
}
