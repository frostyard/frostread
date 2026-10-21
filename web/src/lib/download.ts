// A download of a text file, from the browser alone: the OPML export
// (docs/specs/api.md, opml.export) and nothing else needs one.
/** downloadFile offers name to the browser with text as its body, and returns its link. */
export function downloadFile(name: string, text: string, type: string): HTMLAnchorElement {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.rel = "noopener";
  link.click();
  // The browser has started the download; give it the blob before the URL
  // stops naming it.
  setTimeout(() => URL.revokeObjectURL(url), 1_000);
  return link;
}

/** downloadOPML offers the OPML export as frostread.opml. */
export function downloadOPML(xml: string): HTMLAnchorElement {
  return downloadFile("frostread.opml", xml, "text/x-opml");
}
