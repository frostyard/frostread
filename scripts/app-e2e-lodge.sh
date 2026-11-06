#!/bin/sh
# Run the desktop lodge e2e suite against lodge's development build.
# The caller builds the app and the Go e2e binaries, then runs this script
# in the app nsl machine so lodge, Notes, Read and WebKit share loopback.
set -eu

repo=$(cd "$(dirname "$0")/.." && pwd)
# The nsl machine sees the host's home under another path, so a sibling
# checkout is best named relative to this repository, as dev-lodge's
# defaults are (../lodge and ../frostnotes).
lodge=${LODGE_DIR:-$repo/../lodge}
notes=${NOTES_DIR:-$repo/../frostnotes}
for dir in "$lodge" "$notes"; do
  [ -d "$dir" ] || {
    echo "app-e2e-lodge: $dir is not a directory here; give LODGE_DIR and NOTES_DIR relative to the repository" >&2
    exit 1
  }
done

if [ ! -f "$repo/build/web/index.html" ]; then
  echo "app-e2e-lodge: frostread/build/web is missing; build it with: make web-build # nsl" >&2
  exit 1
fi

root=$(mktemp -d "${TMPDIR:-/tmp}/frostread-app-lodge.XXXXXX")
log=$root/dev-lodge.log
dev_pid=

end_processes() {
  if [ -n "$dev_pid" ] && kill -0 "$dev_pid" 2>/dev/null; then
    kill -TERM "$dev_pid" 2>/dev/null || true
    i=0
    while kill -0 "$dev_pid" 2>/dev/null && [ "$i" -lt 100 ]; do
      i=$((i + 1))
      sleep 0.1
    done
    if kill -0 "$dev_pid" 2>/dev/null; then
      kill -KILL "$dev_pid" 2>/dev/null || true
    fi
  fi
  if [ -n "$dev_pid" ]; then
    wait "$dev_pid" 2>/dev/null || true
  fi

  known='[l]odge-e2e|[n]otesd|[r]eaddev|build/e2e/[r]eadd|[f]rostread-app|[W]ebKitWebDriver'
  pids=$(pgrep -f "$known" 2>/dev/null || true)
  if [ -n "$pids" ]; then
    for pid in $pids; do kill -TERM "$pid" 2>/dev/null || true; done
    i=0
    while pgrep -af -r D,I,R,S,T,t "$known" >/dev/null 2>&1 && [ "$i" -lt 100 ]; do
      i=$((i + 1))
      sleep 0.1
    done
    pids=$(pgrep -af -r D,I,R,S,T,t "$known" 2>/dev/null | awk '{print $1}' || true)
    for pid in $pids; do kill -KILL "$pid" 2>/dev/null || true; done
  fi
}
trap 'end_processes' EXIT INT TERM

LODGE_DIR="$lodge" NOTES_DIR="$notes" LODGE_PORT=8480 \
  scripts/dev-lodge.sh "$root" >"$log" 2>&1 &
dev_pid=$!

i=0
while ! grep -Fq 'lodge (development build): http://127.0.0.1:8480/' "$log"; do
  if ! kill -0 "$dev_pid" 2>/dev/null; then
    echo "app-e2e-lodge: dev-lodge exited before its address block" >&2
    tail -n 100 "$log" >&2 || true
    exit 1
  fi
  i=$((i + 1))
  [ "$i" -le 3000 ] || {
    echo "app-e2e-lodge: dev-lodge did not print its address block in 300 s" >&2
    tail -n 100 "$log" >&2 || true
    exit 1
  }
  sleep 0.1
done

export READ_E2E_LODGE=http://127.0.0.1:8480
export READ_E2E_LODGE_ROOT=$root

cd "$repo/web"
pnpm install --frozen-lockfile --silent
set +e
env -u WAYLAND_DISPLAY GDK_BACKEND=x11 xvfb-run -a -s '-screen 0 1280x800x24' \
  pnpm exec vitest run -c vitest.e2e.config.ts e2e/lodge.e2e.ts
rc=$?
set -e

end_processes
known='[b]uild/e2e/|[f]rostread-app|[W]ebKitWebDriver|[l]odge-e2e|[n]otesd|[r]eaddev'
if pgrep -af -r D,I,R,S,T,t "$known"; then
  echo "app-e2e-lodge: a process of the run is left" >&2
  rc=1
fi

exit "$rc"
