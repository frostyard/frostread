#!/bin/sh
# The Flatpak's smoke test (plan 0002, Phase 4), run last by the frostread
# module's test-commands, and on the host by `make flatpak-smoke`. The
# portal, the app and the Background helper are fakes, since the build
# sandbox has none of them and no display; readd, readctl and the launcher
# are the real ones in FROSTREAD_BIN_DIR (default /app/bin). Nothing reaches
# the network.
#
# It checks, after docs/adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md:
# the launcher starts readd through `flatpak-spawn --latest-version frostread
# --background` and leaves no flatpak-spawn behind; readd answers on the
# Flatpak's socket and turns its log over; the launcher runs the Background
# helper; a second readd exits on the lock, and does not move the log; links
# saved through readctl survive an update, in which a readd whose build is
# not the latest is replaced at the launcher's next run, and one that is the
# latest, or whose latest build is unknown, is kept; a latest-build query
# that leaves a process on its output does not hold the launcher; and in
# lodge mode the launcher stops a local readd and starts neither readd nor
# the helper, and `frostread --background` exits 0. The test cleans up after
# itself.

set -eu

bin="${FROSTREAD_BIN_DIR:-/app/bin}"

work="$(mktemp -d)"
pid=
cleanup() {
    if [ -s "$work/detached.pid" ]; then
        kill -KILL "$(cat "$work/detached.pid")" 2>/dev/null || true
    fi
    if [ -n "$pid" ]; then
        kill -TERM "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
    fi
    rm -rf "$work"
}
trap cleanup EXIT

state="$work/state/frostread"
print_logs() {
    if [ -f "$state/readd.log.1" ]; then
        echo "--- $state/readd.log.1, its end" >&2
        tail -n 20 "$state/readd.log.1" | tr -d '\000' >&2 || true
    fi
    for f in "$state/readd.log" "$work/launcher.log" "$work/background.log"; do
        if [ -f "$f" ]; then
            echo "--- $f" >&2
            cat "$f" >&2
        fi
    done
}

mkdir -p "$work/run" "$work/home" "$work/data" "$work/config" "$work/bin" "$work/libexec" "$state"
chmod 0700 "$work/run"
export XDG_RUNTIME_DIR="$work/run" HOME="$work/home" XDG_DATA_HOME="$work/data"
export XDG_STATE_HOME="$work/state" XDG_CONFIG_HOME="$work/config"
export FLATPAK_ID=org.frostyard.Frostread
export DBUS_SESSION_BUS_ADDRESS=unix:path="$work/no-bus"
unset READ_SOCKET READ_CONFIG READ_FLATPAK_INFO

cat >"$work/bin/flatpak-spawn" <<'EOF'
#!/bin/sh
if [ "${1:-}" != "--latest-version" ]; then
    echo "fake flatpak-spawn: called without --latest-version: $*" | tee -a "$SMOKE_SPAWN_FAULT" >&2
    exit 2
fi
shift
export READ_FLATPAK_INFO="$SMOKE_LATEST_INFO"
if [ "${1:-}" = "frostread" ]; then
    if [ "${2:-}" != "--background" ]; then
        echo "fake flatpak-spawn: a window start must use frostread --background: $*" | tee -a "$SMOKE_SPAWN_FAULT" >&2
        exit 2
    fi
    echo "$$" >"$SMOKE_SPAWN_PID"
    "$@" &
    echo "$!" >"$SMOKE_CHILD_PID"
    wait "$!"
elif [ -n "${SMOKE_SPAWN_DETACH:-}" ] && [ "$*" = "readd -build" ]; then
    setsid sh -c 'echo $$ >"$1"; exec sleep 60' sh "$SMOKE_DETACHED_PID" &
    n=0
    while [ ! -s "$SMOKE_DETACHED_PID" ] && [ "$n" -lt 100 ]; do
        sleep 0.05
        n=$((n + 1))
    done
    exit 0
else
    exec "$@"
fi
EOF
chmod +x "$work/bin/flatpak-spawn"

cat >"$work/bin/readctl" <<'EOF'
#!/bin/sh
if [ -n "${SMOKE_SLOW_SHUTDOWN:-}" ] && [ "${1:-}" = "shutdown" ]; then
    echo "readd stops"
    ( sleep 1; "$SMOKE_REAL_READCTL" shutdown </dev/null >/dev/null 2>&1 ) &
    exit 0
fi
exec "$SMOKE_REAL_READCTL" "$@"
EOF
chmod +x "$work/bin/readctl"
SMOKE_REAL_READCTL="$bin/readctl"
export SMOKE_REAL_READCTL

cat >"$work/libexec/frostread-app" <<'EOF'
#!/bin/sh
# The app must start with no child: the launcher's Background helper
# (which lives 2 s here) must not be one, or it stays a zombie for the
# window's life. read is a builtin, so the check itself forks nothing.
# The file has no newline, so read returns 1 but still sets kids. With no
# such file, the record says so, and the smoke test fails: no observation
# is no pass.
children=/proc/$$/task/$$/children
if [ -r "$children" ]; then
    kids=
    read -r kids <"$children" || true
    printf 'children:%s\n' "$kids" >"$SMOKE_APP_CHILDREN"
else
    printf 'unreadable:%s\n' "$children" >"$SMOKE_APP_CHILDREN"
fi
exit 0
EOF
cat >"$work/bin/frostread-request-background" <<'EOF'
#!/bin/sh
printf 'args:%s\n' "$*" >>"$SMOKE_REQUEST_ARGS"
sleep 2
EOF
chmod +x "$work/libexec/frostread-app" "$work/bin/frostread-request-background"

SMOKE_SPAWN_PID="$work/spawn.pid"
SMOKE_CHILD_PID="$work/readd.pid"
SMOKE_SPAWN_FAULT="$work/spawn.fault"
SMOKE_DETACHED_PID="$work/detached.pid"
SMOKE_REQUEST_ARGS="$work/request.args"
SMOKE_APP_CHILDREN="$work/app.children"
export SMOKE_SPAWN_PID SMOKE_CHILD_PID SMOKE_SPAWN_FAULT SMOKE_DETACHED_PID SMOKE_REQUEST_ARGS SMOKE_APP_CHILDREN

info() {
    printf '[Application]\napp-commit=not-the-build\n\n[Instance]\napp-commit=%s\n' "$2" >"$1"
}
info "$work/info-a" buildA
info "$work/info-b" buildB
SMOKE_LATEST_INFO="$work/info-a"
export SMOKE_LATEST_INFO
export FROSTREAD_LIBEXEC_DIR="$work/libexec"
PATH="$work/bin:$bin:$PATH"
export PATH

# A readd log over 1 MiB must rotate once the daemon holds the lock.
dd if=/dev/zero of="$state/readd.log" bs=1024 count=1025 2>/dev/null

"$bin/frostread" >"$work/launcher.log" 2>&1

for _ in $(seq 1 20); do
    if [ -s "$SMOKE_CHILD_PID" ]; then
        break
    fi
    sleep 0.25
done
spawned="$(cat "$SMOKE_SPAWN_PID" 2>/dev/null || true)"
pid="$(cat "$SMOKE_CHILD_PID" 2>/dev/null || true)"
if [ -z "$pid" ]; then
    echo "smoke test: the launcher did not start readd through flatpak-spawn" >&2
    print_logs
    exit 1
fi
if [ -n "$spawned" ] && kill -0 "$spawned" 2>/dev/null; then
    echo "smoke test: flatpak-spawn $spawned outlived the launcher" >&2
    print_logs
    exit 1
fi

ready=
for _ in $(seq 1 40); do
    if readctl status >/dev/null 2>&1; then
        ready=1
        break
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
        break
    fi
    sleep 0.5
done
if [ -z "$ready" ]; then
    echo "smoke test: readd did not answer within 20 s" >&2
    print_logs
    exit 1
fi

if [ ! -f "$state/readd.log.1" ]; then
    echo "smoke test: the old readd log did not move to readd.log.1" >&2
    print_logs
    exit 1
fi
size="$(wc -c <"$state/readd.log")"
if [ "$size" -gt 1048576 ]; then
    echo "smoke test: readd.log holds $size bytes; want a new log of at most 1 MiB" >&2
    print_logs
    exit 1
fi

socket="$XDG_RUNTIME_DIR/app/org.frostyard.Frostread/frostread/readd.sock"
if [ ! -S "$socket" ]; then
    echo "smoke test: no socket at $socket" >&2
    find "$XDG_RUNTIME_DIR" >&2
    print_logs
    exit 1
fi
if [ ! -s "$SMOKE_REQUEST_ARGS" ]; then
    for _ in $(seq 1 50); do
        [ -s "$SMOKE_REQUEST_ARGS" ] && break
        sleep 0.1
    done
fi
if [ ! -s "$SMOKE_REQUEST_ARGS" ]; then
    echo "smoke test: the launcher did not run the Background helper" >&2
    print_logs
    exit 1
fi
seen=
if [ -r "$SMOKE_APP_CHILDREN" ]; then
    IFS= read -r seen <"$SMOKE_APP_CHILDREN" || true
fi
case "$seen" in
children:)
    ;;
children:*)
    echo "smoke test: the Background helper is a child of the app (${seen#children:})" >&2
    print_logs
    exit 1
    ;;
*)
    echo "smoke test: the app's children were not observed (${seen:-no record}); the check needs /proc/PID/task/TID/children" >&2
    print_logs
    exit 1
    ;;
esac

# A second --background reaches readd's lock and exits non-zero.
dd if=/dev/zero bs=1024 count=1025 2>/dev/null >>"$state/readd.log"
rc=0
"$bin/frostread" --background >/dev/null 2>>"$work/background.log" || rc=$?
if [ "$rc" -eq 0 ]; then
    echo "smoke test: a second frostread --background exited 0; want the lock's error" >&2
    print_logs
    exit 1
fi
if [ ! -f "$state/readd.log" ]; then
    echo "smoke test: the running readd log was moved" >&2
    print_logs
    exit 1
fi
if ! tail -n 5 "$state/readd.log" | grep -q "another readd uses"; then
    echo "smoke test: readd.log does not end with the lock's line" >&2
    print_logs
    exit 1
fi
size="$(wc -c <"$state/readd.log")"
if [ "$size" -le 1048576 ]; then
    echo "smoke test: readd.log holds $size bytes; want the running readd's log, over 1 MiB" >&2
    print_logs
    exit 1
fi

rc=0
timeout 10 readd -desktop --socket "$work/second.sock" >"$work/second.log" 2>&1 || rc=$?
if [ "$rc" -eq 0 ] || [ "$rc" -eq 124 ] || ! grep -q "another readd uses" "$work/second.log"; then
    echo "smoke test: a second readd exited with $rc, not an another-readd error" >&2
    cat "$work/second.log" >&2
    print_logs
    exit 1
fi

# Two saved links, with no fetch: an OPML import fetches each feed, and the
# build has no network. The rooted `.onion.` names do not query DNS: Go
# recognizes `.onion` as special and a trailing root dot keeps the name
# from getting search-domain suffixes. The extraction follows, but does
# not resolve these names. The ids show after the update that the same
# database is open: a new one would number the second link 1.
for n in one two; do
    readctl save "https://$n.smoke.onion./article" >>"$work/saved.log" 2>&1 || {
        echo "smoke test: readctl save of the $n link failed" >&2
        cat "$work/saved.log" >&2
        print_logs
        exit 1
    }
done
second=$(sed -n 's/^saved \([0-9][0-9]*\) .*/\1/p' "$work/saved.log" | tail -n 1)
if [ "$second" != "2" ]; then
    echo "smoke test: the second link was saved as '$second', not 2" >&2
    cat "$work/saved.log" >&2
    print_logs
    exit 1
fi

got=$(readctl status)
printf '%s\n' "$got" | grep -qx "build buildA" || {
    echo "smoke test: readd reports status '$got', not build buildA" >&2
    print_logs
    exit 1
}

wait_gone() {
    for _ in $(seq 1 40); do
        kill -0 "$1" 2>/dev/null || return 0
        sleep 0.25
    done
    return 1
}

# An update stops build A and starts build B, on the same database.
old=$pid
rm -f "$SMOKE_SPAWN_PID" "$SMOKE_CHILD_PID"
SMOKE_LATEST_INFO="$work/info-b"
SMOKE_SLOW_SHUTDOWN=1
export SMOKE_LATEST_INFO SMOKE_SLOW_SHUTDOWN
"$bin/frostread" >"$work/launcher.log" 2>&1
for _ in $(seq 1 20); do
    [ -s "$SMOKE_CHILD_PID" ] && break
    sleep 0.25
done
if [ ! -s "$SMOKE_CHILD_PID" ]; then
    echo "smoke test: the launcher did not start readd of the latest build" >&2
    print_logs
    exit 1
fi
spawned=$(cat "$SMOKE_SPAWN_PID" 2>/dev/null || true)
pid=$(cat "$SMOKE_CHILD_PID")
if [ "$pid" = "$old" ] || ! wait_gone "$old"; then
    echo "smoke test: the old readd $old still runs" >&2
    print_logs
    exit 1
fi
if [ -n "$spawned" ] && kill -0 "$spawned" 2>/dev/null; then
    echo "smoke test: flatpak-spawn $spawned outlived the update launcher" >&2
    print_logs
    exit 1
fi
printf '%s\n' "$(readctl status)" | grep -qx "build buildB" || {
    echo "smoke test: replacement does not report buildB" >&2
    print_logs
    exit 1
}
unset SMOKE_SLOW_SHUTDOWN
got=$(readctl save "https://two.smoke.onion./article" 2>&1 || true)
if [ "$got" != "saved 2 two.smoke.onion." ]; then
    echo "smoke test: after the update the second link answers '$got', not 'saved 2 two.smoke.onion.'" >&2
    print_logs
    exit 1
fi
sleep 1
if ! kill -0 "$pid" 2>/dev/null; then
    echo "smoke test: the replacement readd $pid ended with flatpak-spawn" >&2
    print_logs
    exit 1
fi

# The latest build is kept; an unknown latest build is kept too.
rm -f "$SMOKE_SPAWN_PID" "$SMOKE_CHILD_PID"
"$bin/frostread" >"$work/launcher.log" 2>&1
if [ -e "$SMOKE_SPAWN_PID" ] || ! kill -0 "$pid" 2>/dev/null || ! readctl status >/dev/null 2>&1; then
    echo "smoke test: the launcher did not keep readd of the latest build" >&2
    print_logs
    exit 1
fi

SMOKE_LATEST_INFO="$work/no-info"
export SMOKE_LATEST_INFO
"$bin/frostread" >"$work/launcher.log" 2>&1
if [ -e "$SMOKE_SPAWN_PID" ] || ! kill -0 "$pid" 2>/dev/null || ! readctl status >/dev/null 2>&1; then
    echo "smoke test: the launcher did not keep readd when the latest build was unknown" >&2
    print_logs
    exit 1
fi

# A detached latest-build query leaves no process or file that the launcher
# waits on, and keeps readd.
SMOKE_LATEST_INFO="$work/info-b"
SMOKE_SPAWN_DETACH=1
export SMOKE_LATEST_INFO SMOKE_SPAWN_DETACH
rm -f "$SMOKE_SPAWN_PID" "$SMOKE_CHILD_PID"
start=$(date +%s)
rc=0
"$bin/frostread" >"$work/launcher.log" 2>&1 || rc=$?
took=$(($(date +%s) - start))
unset SMOKE_SPAWN_DETACH
detached=$(cat "$SMOKE_DETACHED_PID" 2>/dev/null || true)
if [ -z "$detached" ] || ! kill -0 "$detached" 2>/dev/null; then
    echo "smoke test: the latest-build query did not leave a detached process" >&2
    print_logs
    exit 1
fi
if [ "$rc" -ne 0 ] || [ "$took" -gt 20 ]; then
    echo "smoke test: the launcher exited $rc after $took s beside a detached query" >&2
    print_logs
    exit 1
fi
if [ -e "$SMOKE_SPAWN_PID" ] || ! kill -0 "$pid" 2>/dev/null || ! readctl status >/dev/null 2>&1; then
    echo "smoke test: the launcher did not keep readd beside the detached query" >&2
    print_logs
    exit 1
fi
if ls "$state"/latest-build.* >/dev/null 2>&1; then
    echo "smoke test: the launcher left a latest-build file in $state" >&2
    exit 1
fi
kill -KILL "$detached" 2>/dev/null || true

if [ -e "$SMOKE_SPAWN_FAULT" ]; then
    echo "smoke test: the launcher called flatpak-spawn without --latest-version" >&2
    print_logs
    exit 1
fi

# Lodge mode stops readd, starts no readd or helper, and opens no engine.
mkdir -p "$XDG_CONFIG_HOME/frostread"
printf '{"lodge":"https://lodge.example.ts.net"}\n' >"$XDG_CONFIG_HOME/frostread/app.json"
rm -f "$SMOKE_SPAWN_PID" "$SMOKE_CHILD_PID" "$SMOKE_REQUEST_ARGS"
old=$pid
"$bin/frostread" >"$work/launcher.log" 2>&1
if [ -e "$SMOKE_SPAWN_PID" ] || [ -e "$SMOKE_REQUEST_ARGS" ] || ! wait_gone "$old"; then
    echo "smoke test: lodge mode left readd or a local helper running" >&2
    print_logs
    exit 1
fi
if readctl status >/dev/null 2>&1; then
    echo "smoke test: lodge mode left readd answering status" >&2
    print_logs
    exit 1
fi
rc=0
"$bin/frostread" --background >/dev/null 2>&1 || rc=$?
if [ "$rc" -ne 0 ]; then
    echo "smoke test: frostread --background exited $rc in lodge mode; want 0" >&2
    print_logs
    exit 1
fi
if readctl status >/dev/null 2>&1; then
    echo "smoke test: frostread --background started readd in lodge mode" >&2
    print_logs
    exit 1
fi
rm -f "$XDG_CONFIG_HOME/frostread/app.json"

if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill -TERM "$pid"
    wait_gone "$pid" || true
fi
pid=
echo "smoke test passed"
