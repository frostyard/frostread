// The shell the app runs in, as the components see it: lodge's frame, or the
// desktop's window. The components call only this interface, through
// useShell(); the default is lodge's, so the lodge entry sets nothing.
import { createContext, type ReactNode, useContext } from "react";
import { lodgeShell } from "./lodge.js";

/** ServerInfo is the computer's and lodge's server facts the Server sheet reads. */
export interface ServerInfo {
  lodge: string;
  /**
   * lastLodge is the address the last lodge mode used: local mode offers it
   * again, and lodge mode answers "".
   */
  lastLodge: string;
  localData: boolean;
}

/** ServerControl changes the desktop's connection and reloads the window. */
export interface ServerControl {
  readonly lodge: string;
  /** known is false when the window could not read the mode at its start. */
  readonly known: boolean;
  get(): Promise<ServerInfo>;
  save(lodge: string): Promise<void>;
  reload(): void;
}

/** Shell is what the app asks of the window it runs in. */
export interface Shell {
  /** server is present only in the desktop shell's window. */
  server?: ServerControl;
  /** savePages: the save page, the bookmarklet page and their sidebar entry exist. */
  readonly savePages: boolean;
  /**
   * exportsMarkdown: the reader offers "Export as Markdown…" instead of save
   * as note (the desktop in local mode, ADR-0008).
   */
  readonly exportsMarkdown: boolean;
  /** reportRoute tells the shell the app's route, for its address and history. */
  reportRoute(path: string, title: string, replace: boolean): void;
  /** openRoute opens a route in another app: a note. */
  openRoute(route: string): void;
  /** openOutside opens an article or a link outside the app. */
  openOutside(url: string): void;
  /** saveFile asks the shell to save text with name and MIME type. */
  saveFile(name: string, text: string, type: string): Promise<boolean>;
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
