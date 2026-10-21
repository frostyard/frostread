// One item in the list: the title (bold while unread), its source and date,
// a two-line excerpt or search snippet, and the star and saved marks.

import { cx } from "@frostyard/lodge-ui/react";
import { Bookmark, Star } from "lucide-react";
import { listDate } from "../lib/format.js";
import type { ItemSummary } from "../rpc/api.js";

/** ROW_HEIGHT is the fixed height of a list row in pixels. */
export const ROW_HEIGHT = 84;

/** ItemRow is one row; the list owns focus, selection and virtualization. */
export function ItemRow(props: { item: ItemSummary; selected: boolean; onSelect: () => void }) {
  const { item, selected } = props;
  const sourceTitle = item.sourceTitle ?? "";
  const meta = [sourceTitle === "" ? "Saved link" : sourceTitle, listDate(item.published)]
    .filter((part) => part !== "")
    .join(" · ");
  return (
    // biome-ignore lint/a11y/useKeyWithClickEvents: the list's listbox owns keyboard navigation; the row reports the pointer.
    <div
      role="option"
      tabIndex={-1}
      aria-selected={selected}
      data-item-id={item.id}
      onClick={props.onSelect}
      className={cx(
        "relative h-[84px] cursor-default overflow-hidden py-2 pl-3 pr-3",
        selected ? "bg-selection-inactive" : "bg-window hover:bg-selection-inactive",
      )}
    >
      <div className="absolute inset-x-3 bottom-0 h-px bg-separator" />
      <div className="flex items-baseline gap-2">
        <span
          className={cx(
            "min-w-0 flex-1 truncate text-list-subject",
            item.read ? "font-normal text-secondary" : "font-semibold text-primary",
          )}
        >
          {item.title}
        </span>
        {item.starred && (
          <span role="img" className="shrink-0 text-flag-3" aria-label="Starred">
            <Star size={12} aria-hidden="true" />
          </span>
        )}
        {item.saved && (
          <span role="img" className="shrink-0 text-accent" aria-label="Saved">
            <Bookmark size={12} aria-hidden="true" />
          </span>
        )}
      </div>
      <div className="truncate text-list-date text-secondary">{meta}</div>
      <div className="line-clamp-2 text-list-preview text-secondary">
        <Snippet text={item.snippet ?? item.excerpt ?? ""} />
      </div>
    </div>
  );
}

/** Snippet shows search results' ** marked words as bold. */
export function Snippet(props: { text: string }) {
  const parts = props.text.split("**");
  return (
    <>
      {parts.map((part, index) =>
        index % 2 === 1 ? (
          // biome-ignore lint/suspicious/noArrayIndexKey: the parts of one snippet never move.
          <strong key={index}>{part}</strong>
        ) : (
          // biome-ignore lint/suspicious/noArrayIndexKey: the parts of one snippet never move.
          <span key={index}>{part}</span>
        ),
      )}
    </>
  );
}
