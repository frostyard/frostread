# The hostile corpus

Markup that tries to make a request, run a script or open a window. Each
file is one page, and `web/e2e/hostile.e2e.ts` shows every page two ways
(plan [0002](../../../docs/plans/0002-the-standalone-desktop-flatpak.md),
step 3.7):

1. **Raw in the frame:** the harness sets the reader's `srcdoc` to the file,
   so only the sandbox (`sandbox="allow-popups"`) and the app's CSP stand
   between the page and the network.
2. **Through readd:** `cmd/readdev -hostile DIR` serves the folder as the
   `Dev Hostile` feed, so the sanitizer runs too.

Neither way may reach the canary, and no script or event handler may run.

## The canary

Every URL that points at the canary is written `http://canary.test/<path>`
or `//canary.test/<path>`. The harness replaces `canary.test` with a
recording server on loopback before it uses a file, so a recorded request
names its file: the path starts with the file's number and name. The
canary's `host:port` is never in `READ_DEV_ALLOW`, so readd's own fetches
(`internal/fetch`) never reach it either.

`{{APP_ENTRY}}` is the app's built entry script, read from
`build/desktop/desktop.html`: `51-script-gadget.html` is the page that
tries to mount the real app inside the frame.

## The detector

Every script with code, and every event handler, starts with

```js
document.body.append(document.createElement("x-ran"));
```

and only then does its hostile work. No file holds an `x-ran` element in
its markup, so the suite looks for `x-ran` elements in the frame: one means
a script or a handler ran. The one exception is `03-script-in-head.html`,
whose script runs before the body exists and so appends to
`document.documentElement`. A script that loads a file (`src="…"`) cannot
carry the marker; its canary URL is the load test.

`48-ping-area.html` puts `ping` on an `area`, not on an `a`: the suite
clicks every `a` in the frame, and a click on an `a` with `ping` may send
the ping before anything else can stop it.

All the data is made up.
