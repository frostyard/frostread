// The article on the desktop: the page items.render answers, shown in a
// srcdoc frame with no script and no same-origin grant (ADR-0008). A page
// the daemon refuses as too large, or one that fails, shows the excerpt.
import { useEffect, useState } from "react";
import { type ItemDetail, isTooLarge } from "../rpc/api.js";
import { useRead } from "./context.js";

/** FrameState is what the reader shows for the item id: loading, the page,
 * or why there is none. */
type FrameState = { id: number } & (
  | { kind: "loading" }
  | { kind: "page"; html: string; omittedImages: number }
  | { kind: "tooLarge" }
  | { kind: "error"; message: string }
);

/** RenderedArticle is an item's article from items.render, in a sandboxed frame. */
export function RenderedArticle(props: { item: ItemDetail; scheme: "light" | "dark" }) {
  const api = useRead((state) => state.api);
  const id = props.item.id;
  const [state, setFrame] = useState<FrameState>({ id, kind: "loading" });
  // A state of another item is loading: the old item's page never shows
  // under the new one. A scheme change keeps the page until the new one comes.
  const frame: FrameState = state.id === id ? state : { id, kind: "loading" };

  useEffect(() => {
    if (api === null) return;
    // A fast change of item or scheme leaves an answer on its way: the
    // cleanup marks it, and the stale page is never shown.
    let cancelled = false;
    api.renderItem(id, props.scheme).then(
      (rendered) => {
        if (cancelled) return;
        setFrame({ id, kind: "page", html: rendered.html, omittedImages: rendered.omittedImages });
      },
      (err: unknown) => {
        if (cancelled) return;
        if (isTooLarge(err)) setFrame({ id, kind: "tooLarge" });
        else setFrame({ id, kind: "error", message: err instanceof Error ? err.message : String(err) });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [api, id, props.scheme]);

  if (frame.kind === "loading") {
    return <p className="p-[var(--pad-x)] text-secondary">Loading…</p>;
  }

  if (frame.kind === "tooLarge") {
    return (
      <div className="min-h-0 flex-1 overflow-y-auto p-[var(--pad-x)]">
        <p>This article is too large to show in the app.</p>
        <p className="mt-3 whitespace-pre-wrap text-reader-body text-secondary">{props.item.excerpt ?? ""}</p>
        <p className="mt-3">
          <a href={props.item.url} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">
            Open the original
          </a>
        </p>
      </div>
    );
  }

  if (frame.kind === "error") {
    return (
      <div className="min-h-0 flex-1 overflow-y-auto p-[var(--pad-x)]">
        <p className="text-flag-1">{frame.message}</p>
        <p className="mt-3 whitespace-pre-wrap text-reader-body text-secondary">{props.item.excerpt ?? ""}</p>
      </div>
    );
  }

  const omitted = frame.omittedImages;
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {omitted > 0 && (
        <p role="status" className="border-b border-separator px-[var(--pad-x)] py-1 text-sidebar-row text-secondary">
          {omitted === 1
            ? "1 image was left out: the article is too large to show every image in the app."
            : `${omitted} images were left out: the article is too large to show every image in the app.`}
        </p>
      )}
      <iframe
        title={props.item.title}
        srcDoc={frame.html}
        sandbox="allow-popups"
        referrerPolicy="no-referrer"
        className="min-h-0 w-full flex-1 border-0"
      />
    </div>
  );
}
