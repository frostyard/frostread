# 0009 — readd stops when the GNOME session ends

- **Status:** Accepted (Brian, 2026-11-13)
- **Date:** 2026-11-13
- **Decided:** Brian chose on 2026-11-13 that readd exits at logout, with
  the sandbox permission that it needs
  ([frostread#81](https://github.com/frostyard/frostread/issues/81)).
  This ADR picks the narrowest permission that does it.

## Context

[ADR-0008](0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md)
says that readd in local mode polls in the background "until logout". The
built readd does not watch the session. The launcher has the Flatpak
portal start `frostread --background` in a sandbox of its own, so readd
lives as long as the user's systemd instance and its session bus, not as
long as the graphical session.

The clean-install gate of
[plan 0002, Phase 10](../plans/0002-the-standalone-desktop-flatpak.md)
recorded this on `v0.6.0-rc.4` and on `v0.6.0-rc.6` (finding 3). With no
other session, readd stopped 26 s after `gnome-session-quit --logout`.
While an SSH session kept the user's systemd instance, readd ran on after
the logout, and stopped only when that instance ended. A user with
lingering (`loginctl enable-linger`), or with a remote session, has readd
polling the network with no window and no graphical session.

The sandbox has 5 `finish-args` today: Wayland, X11 as a fallback, IPC,
DRI and the network. It sees no D-Bus name outside the portals. Every way
to know when the graphical session ends needs 1 more permission:

- logind on the system bus: `--system-talk-name=org.freedesktop.login1`.
- gnome-session on the session bus: `--talk-name=org.gnome.SessionManager`.

Flatpak has no finish-arg that lets an app only see a name: the lowest
level is talk, which also lets the app call the name's methods.

## Decision

- **The permission.** The manifest's `finish-args` gain
  `--talk-name=org.gnome.SessionManager`, and no other. It is the
  narrowest of the two:
  - It is 1 name on the user's own session bus. logind is on the system
    bus, and its talk level reaches every user's sessions and the
    machine's power actions (`PowerOff`, `Reboot`, `Suspend`), which
    polkit allows with no password to an active session on most
    distributions.
  - The name is on the user's session bus, but its methods are not all
    limited to that session: `Logout` ends it, and `Shutdown` and
    `Reboot` ask for a shutdown or a reboot of the machine. So the
    permission is not read only; readd calls none of these methods.
  - gnome-session owns its name for the life of the graphical session.
    logind keeps an SSH session as a session, so readd would have to
    tell the sessions' types apart to know that the graphical one ended.
- **The watch.** In desktop mode and local mode, readd connects to the
  session bus at `DBUS_SESSION_BUS_ADDRESS` before it serves its socket.
  It subscribes to the bus's `NameOwnerChanged` for
  `org.gnome.SessionManager`, then asks the name's owner. When that owner
  leaves the bus, or the bus connection ends, readd stops as
  `daemon.shutdown` stops it: the poller, the extraction workers and the
  sweep end, then the database closes and the lock goes. It exits 0.
- **No GNOME session.** When `DBUS_SESSION_BUS_ADDRESS` is empty or not a
  `unix:` address, when the name has no owner at start, or when the bus
  cannot be reached, readd logs it once and runs as before: until it is
  stopped, or its session bus ends. readd starts no watch later.
- **A bounded start.** The dial, the authentication, `Hello`, the match
  rule and the owner lookup together have 10 s, and readd's own stop
  ends them sooner: the end of that bound closes the socket. A bus that
  does not answer in time counts as a bus that cannot be reached.
  readd's log never names the bus address or its socket path.
- **Only the bus calls readd needs.** readd calls `AddMatch` and
  `GetNameOwner` on the bus itself, and no method of
  `org.gnome.SessionManager`. It does not register as a client of the
  session, so it never delays a logout.
- **One package.** The D-Bus library, `github.com/godbus/dbus/v5`, is
  only in `internal/gnomesession`, as AGENTS.md wants for each heavy
  dependency.
- **Lodge's readd is unchanged.** It runs under systemd on lodge's host,
  and watches no session.

This amends in part ADR-0008's permissions (`finish-args`) and its
background lifecycle (readd's "until logout" now holds on GNOME).

## Consequences

- On GNOME, readd stops at logout also with lingering or another session
  open. At the next login, the Background portal's autostart starts it
  again, as before.
- On a desktop that is not GNOME, nothing changes: readd lives as long as
  the user's session bus.
- The sandbox gains a talk permission to gnome-session. Code in the
  sandbox can ask gnome-session to log the user out, or to shut down or
  reboot the machine.
  The permission list and `internal/packagecheck`'s test of the manifest
  change together.
- A restart of gnome-session alone stops readd. The next window, or the
  next login, starts it again.
- readd keeps 1 more connection open: to the session bus, through
  Flatpak's D-Bus proxy. It is a client connection: readd still listens
  on no network port and on no socket but its private one.
- The Go module gains `github.com/godbus/dbus/v5`, so `go-sources.json`
  changes with it.
- The tests start their own `dbus-daemon` (`internal/bustest`), so the Go
  tests need `dbus-daemon` on the machine that runs them.
- The logout through Flatpak's D-Bus proxy has no automated test against
  a real GNOME session. The next release's clean-install gate checks it
  on a real desktop, with an SSH session open through the logout.

## Alternatives considered

- **logind on the system bus** (`--system-talk-name=org.freedesktop.login1`):
  desktop-neutral, but a system bus permission that reaches the machine's
  power actions and every user's sessions, and logind counts an SSH
  session as a session. Rejected as broader.
- **The Inhibit portal's `CreateMonitor`:** it needs no finish-arg, but
  its session states come through the portal backend's own client of
  gnome-session, its "query end" state wants an answer
  (`QueryEndResponse`), and a test needs a stand-in portal. Rejected for
  now; a later ADR can move to it when a real desktop shows that it
  reports the end of a session that a lingering user instance outlives.
- **Keep the behaviour, and change ADR-0008's words** to "until the user's
  session bus ends" (frostread#81, direction 2): readd would poll after
  logout for a user with lingering. Rejected.
- **Register readd as a client of gnome-session** and stop at
  `EndSession`: readd would take part in the logout, and a slow answer
  delays it. The name's owner leaving the bus is enough. Rejected.

## References

- Amends in part: [ADR-0008](0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md) (the Flatpak's permissions and background lifecycle)
- Shapes: [design/overview.md](../design/overview.md) (the Flatpak), `packaging/flatpak/org.frostyard.Frostread.json`, `internal/gnomesession`, `cmd/readd`
- Evidence: [plan 0002, Phase 10](../plans/0002-the-standalone-desktop-flatpak.md) (finding 3 of `v0.6.0-rc.4`, and `v0.6.0-rc.6`)
- Issue: [frostread#81](https://github.com/frostyard/frostread/issues/81)
