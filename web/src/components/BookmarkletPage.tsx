// The page that hands out the bookmarklet (ADR-0006): the link to drag, its
// code to copy, and how to install it in iOS Safari and on a desktop. The
// code is built from the frame's own origin, which is lodge's, so no host is
// written down here.
import { useEffect, useRef, useState } from "react";
import { bookmarkletCode } from "../lib/bookmarklet.js";
import { UNREAD } from "../lib/routes.js";
import { useRead } from "./context.js";

/** BookmarkletPage shows the bookmarklet and its install steps. origin is
 * the frame's origin; a test passes its own. */
export function BookmarkletPage(props: { origin?: string }) {
  const setRoute = useRead((state) => state.setRoute);
  const origin = props.origin ?? window.location.origin;
  const code = bookmarkletCode(origin);
  const link = useRef<HTMLAnchorElement>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [copied, setCopied] = useState("Copy");

  // React 19 refuses a javascript: href in JSX, so it is set on the element
  // itself, where a browser keeps it as the bookmark's address.
  useEffect(() => {
    link.current?.setAttribute("href", code);
  }, [code]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied("Copied");
    } catch {
      setCopied("Select the text and copy it");
    }
  };

  return (
    <section aria-label="Save from your phone" className="flex h-full flex-col overflow-y-auto bg-window text-primary">
      <div className="mx-auto flex w-full max-w-xl flex-col gap-4 px-[var(--pad-x)] py-6">
        <h1 className="text-empty font-semibold">Save from your phone</h1>
        <p className="text-reader-body">
          Use this bookmarklet to save the page you are on to Read. It opens Read&apos;s save page, where you tap Save.
        </p>

        <div className="flex flex-col gap-2 rounded-md border border-separator p-3">
          <a
            ref={link}
            href="/bookmarklet"
            onClick={(event) => {
              event.preventDefault();
              setNotice("Drag this link to your bookmarks bar.");
            }}
            className="w-fit rounded-md bg-accent px-4 py-3 text-sidebar-row font-medium text-accent-contrast hover:opacity-90"
          >
            Save to Read
          </a>
          {notice !== null && (
            <p role="status" className="text-reader-meta text-secondary">
              {notice}
            </p>
          )}
          <textarea
            readOnly
            rows={3}
            aria-label="Bookmarklet code"
            value={code}
            onFocus={(event) => event.currentTarget.select()}
            className="w-full resize-none break-all rounded-md border border-separator bg-window p-2 font-mono text-reader-meta text-primary outline-none focus-visible:border-accent"
          />
          <div>
            <button type="button" onClick={() => void copy()} className={secondary}>
              {copied}
            </button>
          </div>
        </div>

        <h2 className="text-reader-body font-semibold">iPhone and iPad (Safari)</h2>
        <ol className="list-decimal space-y-2 pl-5 text-reader-body">
          <li>Tap Copy above to copy the bookmarklet.</li>
          <li>
            In Safari, open any page, tap the Share button, and tap &quot;Add Bookmark&quot;. Name it &quot;Save to
            Read&quot; and tap Save.
          </li>
          <li>
            Open the bookmarks (the book icon), tap Edit, and tap the &quot;Save to Read&quot; bookmark. It may be in
            Favorites.
          </li>
          <li>Clear its address, paste the copied text, and tap Done.</li>
          <li>
            To save a page: open the page, open the bookmarks, and tap &quot;Save to Read&quot;. Or type &quot;Save to
            Read&quot; in the address bar and tap the bookmark in the suggestions.
          </li>
          <li>Read shows the page&apos;s title and address. Tap Save.</li>
        </ol>

        <h2 className="text-reader-body font-semibold">Desktop browsers</h2>
        <p className="text-reader-body">
          Drag the &quot;Save to Read&quot; link above to your bookmarks bar. Or add a bookmark and paste the code as
          its address.
        </p>

        <p className="text-reader-meta text-secondary">
          The phone must be on the tailnet, as for Read itself. The bookmarklet holds no password or token. Read asks
          before it saves, so no page can save to your list without your tap.
        </p>

        <div>
          <button type="button" onClick={() => void setRoute(UNREAD)} className={secondary}>
            Back to Read
          </button>
        </div>
      </div>
    </section>
  );
}

/** secondary is the page's button, as a sheet's cancel is styled. */
const secondary =
  "h-11 min-w-24 rounded-md border border-separator px-4 text-sidebar-row text-primary hover:bg-selection-inactive";
