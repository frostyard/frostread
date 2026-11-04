#!/bin/sh
# Frostread's launcher in the Flatpak
# (docs/adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md).
# The manifest installs it as /app/bin/frostread, the manifest's command,
# and the Tauri app as /app/libexec/frostread-app.
#
#   frostread --background   Run readd and nothing else: the window's start
#                            through the portal, and the Background
#                            portal's autostart at login. readd takes the
#                            state directory's lock (a second one exits at
#                            once), turns readd.log over past 1 MiB once it
#                            holds the lock, and exits 0 in lodge mode.
#   frostread [ARGS]         In local mode: make sure the latest readd runs,
#                            ask the Background portal for background and
#                            autostart, and run the app. In lodge mode: stop
#                            a local readd that answers, and run the app.
#
# The mode comes from `readd -desktop -mode`, which reads app.json; readd's
# own check under its lock is the one that counts. FROSTREAD_LIBEXEC_DIR,
# when set, is the directory of frostread-app: /app/libexec in the Flatpak,
# and the smoke test's own directory there (packaging/flatpak/smoke-test.sh).
#
# A readd that the window's sandbox started would die with the sandbox. So
# the launcher has the Flatpak portal start `frostread --background` in a new
# sandbox of this app, from the newest installed deployment
# (--latest-version), and ends the flatpak-spawn process once readd answers.
# An update replaces readd, only forward: when the running readd's build
# (readctl status) is not the latest deployment's (readd -build, run the same
# way), the launcher calls daemon.shutdown (readctl shutdown) and starts the
# latest. Every call that waits on readd or on the portal is bounded.

set -u

libexec="${FROSTREAD_LIBEXEC_DIR:-/app/libexec}"
app="$libexec/frostread-app"

state="${XDG_STATE_HOME:-$HOME/.local/state}/frostread"

# bounded SECONDS COMMAND...: run the command for at most SECONDS.
# Every call that waits on readd or the portal has a bound.
if command -v timeout >/dev/null 2>&1; then
    bounded() {
        secs=$1
        shift
        timeout --kill-after=1 "$secs" "$@"
    }
else
    bounded() {
        shift
        "$@"
    }
fi

status() {
    bounded 2 readctl status >/dev/null 2>&1
}

run_readd() {
    mkdir -p "$state"
    # readd rotates the log itself, with -log, once it holds the lock.
    # A duplicate readd must not move the log that the running readd uses.
    # shellcheck disable=SC2094
    exec readd -desktop -log "$state/readd.log" >>"$state/readd.log" 2>&1
}

if [ "${1:-}" = "--background" ]; then
    run_readd
fi

mkdir -p "$state"

have_spawn=
if command -v flatpak-spawn >/dev/null 2>&1; then
    have_spawn=1
fi

mode=$(bounded 5 readd -desktop -mode 2>/dev/null) || mode=local

# Lodge mode does not start this computer's readd, and stops one that a
# crash or a stale autostart left running (ADR-0008, step 2). The wait for
# the data lock (step 3) is the window's, with the lodge connection
# (plan 0002, Phase 6).
if [ "$mode" = "lodge" ]; then
    if status; then
        bounded 35 readctl shutdown >>"$state/readd.log" 2>&1 || true
    fi
    exec "$app" "$@"
fi

# In local mode, replace an old readd only when the latest build is known.
if status && [ -n "$have_spawn" ]; then
    running=$(bounded 2 readctl status 2>/dev/null | sed -n 's/^build //p') || running=
    latest=
    latest_file=$(mktemp "$state/latest-build.XXXXXX" 2>/dev/null) || latest_file=
    if [ -n "$latest_file" ]; then
        if bounded 10 flatpak-spawn --latest-version readd -build </dev/null >"$latest_file" 2>/dev/null; then
            read -r latest <"$latest_file" || true
        fi
        rm -f "$latest_file"
    fi
    if [ -n "$running" ] && [ -n "$latest" ] && [ "$running" != "$latest" ]; then
        bounded 35 readctl shutdown >>"$state/readd.log" 2>&1 || true

        # readctl shutdown answers while readd is stopping. The old readd
        # can answer status briefly after that, so do not let it skip the
        # start block. Wait until status fails; then only a replacement
        # readd that holds the lock can answer status.
        deadline=$(($(date +%s) + 10))
        while [ "$(date +%s)" -lt "$deadline" ] && status; do
            sleep 0.25
        done
    fi
fi

# Start readd through the portal, for about 10 s, until it answers.
# readctl shutdown returns when readd answers, not when it has released its
# lock: a readd started too early exits with "another readd uses", and its
# flatpak-spawn ends. So a spawn that has ended is started again, until the
# deadline.
if ! status; then
    spawned=
    if [ -n "$have_spawn" ]; then
        flatpak-spawn --latest-version frostread --background </dev/null >>"$state/readd.log" 2>&1 &
        spawned=$!

        deadline=$(($(date +%s) + 10))
        while [ "$(date +%s)" -lt "$deadline" ] && ! status; do
            if [ -z "$spawned" ] || ! kill -0 "$spawned" 2>/dev/null; then
                flatpak-spawn --latest-version frostread --background </dev/null >>"$state/readd.log" 2>&1 &
                spawned=$!
            fi
            sleep 0.5
        done

        # flatpak-spawn forwards catchable signals to the command it spawned.
        # Only SIGKILL ends flatpak-spawn alone. The portal keeps readd on
        # without --watch-bus, and readd holds its own stdout and stderr.
        # End the portal process on every path before the app starts.
        if [ -n "$spawned" ]; then
            kill -KILL "$spawned" 2>/dev/null || true
            wait "$spawned" 2>/dev/null || true
        fi

        if ! status; then
            msg="frostread: readd did not start through the Flatpak portal (flatpak-spawn); see $state/readd.log"
            echo "$msg" >&2
            echo "$msg" >>"$state/readd.log"
        fi
    else
        msg="frostread: readd did not start through the Flatpak portal (flatpak-spawn); see $state/readd.log"
        echo "$msg" >&2
        echo "$msg" >>"$state/readd.log"
    fi
fi

frostread-request-background >>"$state/background.log" 2>&1 &
exec "$app" "$@"
