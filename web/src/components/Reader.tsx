// The reader: the item's title, source, author and date, its actions, and
// its article in lodge's content frame. Save as note writes the item into
// notes and then opens it there (docs/specs/api.md, items.saveAsNote).
import { IconButton, iconButtonClass, useNarrow } from "@frostyard/lodge-ui/react";
import { Archive, ArrowLeft, ExternalLink, Mail, MailOpen, Star } from "lucide-react";
import { headerDate } from "../lib/format.js";
import { contentSrc, openRoute } from "../lodge.js";
import { useRead } from "./context.js";

/** Reader is the right pane, or the narrow window's item. */
export function Reader(props: { onBack?: () => void }) {
  const item = useRead((state) => state.item);
  const loading = useRead((state) => state.itemLoading);
  const status = useRead((state) => state.status);
  const busy = useRead((state) => state.busy);
  const scheme = useRead((state) => state.scheme);
  const selectedId = useRead((state) => state.selectedId);
  const noteRoute = useRead((state) => (selectedId === null ? null : state.noteRoute(selectedId)));
  const toggleStar = useRead((state) => state.toggleStar);
  const toggleRead = useRead((state) => state.toggleRead);
  const archive = useRead((state) => state.archive);
  const saveAsNote = useRead((state) => state.saveAsNote);
  const narrow = useNarrow();

  if (item === null) {
    return (
      <section aria-label="Reader" className="flex h-full items-center justify-center bg-window">
        <p className="text-empty text-tertiary">{loading ? "Loading…" : "Select an item to read"}</p>
      </section>
    );
  }

  const meta = [item.sourceTitle, item.author, headerDate(item.published)]
    .filter((part): part is string => part !== undefined && part !== "")
    .join(" · ");
  const configured = status?.notes.configured === true;
  const src = contentSrc(item.contentPath, scheme);

  const title = (
    <div className="min-w-0 flex-1">
      <h1 className="truncate text-reader-subject font-semibold text-primary">{item.title}</h1>
      <p className="truncate text-reader-meta text-secondary">{meta}</p>
    </div>
  );
  const star = (
    <IconButton
      label={item.starred ? "Unstar" : "Star"}
      keys="S"
      icon={Star}
      pressed={item.starred}
      onClick={() => void toggleStar()}
    />
  );
  // A narrow window has no room for the title beside every action: the
  // title and the star take the first row, the rest the second.
  const rest = (
    <>
      <IconButton
        label={item.read ? "Mark unread" : "Mark read"}
        keys="M"
        icon={item.read ? MailOpen : Mail}
        onClick={() => void toggleRead()}
      />
      <IconButton label="Archive" keys="E" icon={Archive} onClick={() => void archive()} />
      <a
        href={item.url}
        target="_blank"
        rel="noopener noreferrer"
        aria-label="Open original"
        title="Open original (O)"
        className={iconButtonClass(28)}
      >
        <ExternalLink size={16} aria-hidden="true" />
      </a>
      <button
        type="button"
        disabled={!configured || busy}
        title={configured ? undefined : "Notes is not set up: see readctl notes-token"}
        onClick={() => {
          if (noteRoute !== null) openRoute(noteRoute);
          else void saveAsNote(item.id);
        }}
        className="ml-1 h-7 rounded-md border border-separator px-2 text-sidebar-row text-primary hover:bg-selection-inactive disabled:opacity-40 disabled:hover:bg-transparent"
      >
        {noteRoute !== null ? "Open in Notes" : "Save as note"}
      </button>
    </>
  );

  return (
    <section aria-label="Reader" className="flex h-full flex-col bg-window">
      {narrow ? (
        <header className="flex flex-col gap-1 border-b border-separator px-[var(--pad-x)] py-2">
          <div className="flex items-center gap-1">
            {props.onBack !== undefined && <IconButton label="Back" icon={ArrowLeft} onClick={props.onBack} />}
            {title}
            {star}
          </div>
          <div className="flex items-center gap-0.5 pl-1">{rest}</div>
        </header>
      ) : (
        <header className="flex items-start gap-2 border-b border-separator px-[var(--pad-x)] py-2">
          {props.onBack !== undefined && <IconButton label="Back" icon={ArrowLeft} onClick={props.onBack} />}
          {title}
          <div className="flex shrink-0 items-center gap-0.5">
            {star}
            {rest}
          </div>
        </header>
      )}

      <div className="min-h-0 flex-1">
        {item.extractState === "pending" ? (
          <p className="p-[var(--pad-x)] text-secondary">Extracting…</p>
        ) : item.extractState === "failed" ? (
          <div className="overflow-y-auto p-[var(--pad-x)]">
            <p className="text-flag-1">{item.extractError ?? "The article could not be extracted."}</p>
            <p className="mt-3 whitespace-pre-wrap text-reader-body text-secondary">{item.excerpt ?? ""}</p>
          </div>
        ) : src === null ? (
          <div className="h-full overflow-y-auto p-[var(--pad-x)]">
            <p className="whitespace-pre-wrap text-reader-body text-secondary">{item.excerpt ?? ""}</p>
          </div>
        ) : (
          <iframe
            title={item.title}
            src={src}
            sandbox="allow-popups allow-popups-to-escape-sandbox"
            referrerPolicy="no-referrer"
            className="h-full w-full border-0"
          />
        )}
      </div>
    </section>
  );
}
