// The bookmarklet that saves the page in the tab it is used on (ADR-0006,
// docs/specs/api.md, Web routes). It only navigates the top-level window to
// lodge's save page, with the page's address and title; it holds no token,
// and it never calls the API.
//
// The frame is same-origin with the shell, so window.location.origin is
// lodge's origin: the code never names a host.

/** SAVE_PATH is lodge's save page, under the app's rooted route. */
const SAVE_PATH = "/read/save?url=";

/** LONGEST is the query the bookmarklet sends, in characters: beyond it the
 * title is left out, so the shell's 2,048-character route still fits. */
const LONGEST = 1900;

/** originOf checks an origin and answers it as a URL writes it. It throws
 * for anything that is not http or https with a host. */
export function originOf(origin: string): string {
  let parsed: URL;
  try {
    parsed = new URL(origin);
  } catch {
    throw new Error(`${JSON.stringify(origin)} is not an address`);
  }
  if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") || parsed.host === "") {
    throw new Error(`${JSON.stringify(origin)} is not an http or https origin`);
  }
  return parsed.origin;
}

/** quote writes a string as a single-quoted JavaScript literal, so a quote
 * in it cannot end the literal early. */
function quote(text: string): string {
  return `'${text.replaceAll("\\", "\\\\").replaceAll("'", "\\'")}'`;
}

/**
 * saveURL is what the bookmarklet computes: the save page's address for a
 * page's href and title. The title is cut to 200 characters, and left out
 * when the query would be too long. bookmarkletCode mirrors it; a test
 * checks that both build the same address.
 */
export function saveURL(origin: string, href: string, title: string): string {
  const base = `${originOf(origin)}${SAVE_PATH}`;
  const url = encodeURIComponent(href);
  let named = encodeURIComponent(title.slice(0, 200));
  if (url.length + named.length > LONGEST) named = "";
  return base + url + (named === "" ? "" : `&title=${named}`);
}

/**
 * bookmarkletCode is the bookmarklet's source, one line: a link whose
 * address is JavaScript that navigates this tab to the save page. origin is
 * the frame's own origin.
 */
export function bookmarkletCode(origin: string): string {
  const base = `${originOf(origin)}${SAVE_PATH}`;
  return (
    "javascript:(function(){var u=encodeURIComponent(location.href)," +
    "t=encodeURIComponent((document.title||'').slice(0,200));" +
    `if(u.length+t.length>${LONGEST})t='';` +
    `location.href=${quote(base)}+u+(t?'&title='+t:'');})();`
  );
}
