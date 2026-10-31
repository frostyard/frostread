// The shell the app runs in, as the components see it: lodge's frame, or the
// desktop's window. The components call only this interface, through
// useShell(); the default is lodge's, so the lodge entry sets nothing.
import { createContext, type ReactNode, useContext } from "react";
import { lodgeShell } from "./lodge.js";

/** Shell is what the app asks of the window it runs in. */
export interface Shell {
  /** savePages: the save page, the bookmarklet page and their sidebar entry exist. */
  readonly savePages: boolean;
  /** reportRoute tells the shell the app's route, for its address and history. */
  reportRoute(path: string, title: string, replace: boolean): void;
  /** openRoute opens a route in another app: a note. */
  openRoute(route: string): void;
  /** openOutside opens an article or a link outside the app. */
  openOutside(url: string): void;
  /**
   * renderArticles: the article comes from items.render into a srcdoc frame
   * (the desktop, ADR-0008), not from lodge-content's contentSrc.
   */
  readonly renderArticles: boolean;
  /** contentSrc is an item's article frame source, or null when there is none. */
  contentSrc(contentPath: string | undefined, scheme?: "light" | "dark"): string | null;
}

const ShellContext = createContext<Shell>(lodgeShell);

/** ShellProvider gives the components their shell. */
export function ShellProvider(props: { shell: Shell; children: ReactNode }) {
  return <ShellContext.Provider value={props.shell}>{props.children}</ShellContext.Provider>;
}

/** useShell is the shell the components run in: lodge's unless a provider says otherwise. */
export function useShell(): Shell {
  return useContext(ShellContext);
}
