#!/bin/sh
# Start lodge's development build on loopback with frostread and frostnotes
# in it, with generated data only, so save-as-note works end to end
# (docs/plans/0001-roadmap.md, Phase 6):
#
#   make dev-lodge
#
# It stages notes and read into a layout root, starts lodge's
# dev/e2e.sh with FAKE_APPS=0, starts notesd as lodge's notes app with a
# generated vault, starts readd with the devfetch escape for the generated
# feeds, issues frostread's scoped notes token, and imports the development
# feeds. Nothing in another checkout is changed: lodge and frostnotes are
# built by their own Makefiles, and this script only reads their outputs.
#
# LODGE_DIR (default ../lodge), NOTES_DIR (default ../frostnotes), ROOT (a
# first argument or a temp dir), LODGE_PORT (8480), LODGE_CONTENT_PORT
# (8481) and DEVFEEDS_ADDR (127.0.0.1:8088) may be set. The token is never
# printed and never written outside the root's state directory.
set -eu

repo=$(cd "$(dirname "$0")/.." && pwd)
lodge=${LODGE_DIR:-$repo/../lodge}
notes=${NOTES_DIR:-$repo/../frostnotes}
lodge=$(cd "$lodge" 2>/dev/null && pwd) || { echo "dev-lodge: LODGE_DIR is not a directory" >&2; exit 1; }
notes=$(cd "$notes" 2>/dev/null && pwd) || { echo "dev-lodge: NOTES_DIR is not a directory" >&2; exit 1; }
root=${1:-${ROOT:-$(mktemp -d "${TMPDIR:-/tmp}/frostread-dev.XXXXXX")}}
mkdir -p "$root"
root=$(cd "$root" && pwd)
port=${LODGE_PORT:-8480}
cport=${LODGE_CONTENT_PORT:-8481}
devfeeds=${DEVFEEDS_ADDR:-127.0.0.1:8088}
logs=$root/logs
mkdir -p "$logs" "$root/state" "$root/vault"
install -d -m 0700 "$root/run/frostnotes" "$root/run/frostread" "$root/state/notes" "$root/state/frostread"

say() { printf 'dev-lodge: %s\n' "$*" >&2; }
die() { say "$*"; exit 1; }

# lodge's development build and lodgectl come from its own Makefile; its
# shell build runs in an nsl machine.
for b in lodge-e2e lodgectl lodge-fakeapp; do
	[ -x "$lodge/build/$b" ] || {
		say "$lodge/build/$b is missing. Build them, and the shell, with:"
		say "  make -C $lodge e2e-build"
		say "  make -C $lodge shell-build   # nsl; without it lodge shows a placeholder shell"
		exit 1
	}
done
[ -f "$lodge/shell/dist/index.html" ] ||
	say "the shell is not built (make -C $lodge shell-build, in nsl): lodge will show a placeholder page"

# notes' web build comes from its own Makefile, in an nsl machine. Without
# it notes has no page in lodge, and lodge disables the app.
[ -f "$notes/build/web/index.html" ] || {
	say "$notes/build/web/index.html is missing. Build notes' web app with:"
	say "  make -C $notes web-build   # nsl"
	exit 1
}
# Always rebuilt: a stale binary from an older checkout may lack -lodge.
say "building notesd and notesctl (Go)"
make -C "$notes" build >"$logs/notes-build.log" 2>&1 ||
	die "make -C $notes build failed; see $logs/notes-build.log"
[ -x "$notes/build/notesd" ] && [ -x "$notes/build/notesctl" ] ||
	die "make -C $notes build left no notesd or notesctl"

# readd with the devfetch escape, for the generated feeds on 127.0.0.1.
say "building and staging frostread (readd with -tags devfetch)"
make -C "$repo" lodge-package LODGE_DESTDIR="$root" TAGS=devfetch >"$logs/frostread-build.log" 2>&1 ||
	die "make lodge-package failed; see $logs/frostread-build.log"
[ -f "$repo/build/web/index.html" ] ||
	say "frostread has no web build (make -C $repo web-build, in nsl): lodge disables its page, and readctl still works"
( cd "$repo" && CGO_ENABLED=0 go build -tags devfeeds -o "$repo/build/readdev" ./cmd/readdev ) ||
	die "build readdev"

# notes' install tree: its manifest and web build, as `make lodge-app` lays
# them out, without rebuilding the web app that is already built.
app=$root/usr/share/lodge/apps/notes
rm -rf "$app"
mkdir -p "$app"
cp -R "$notes/build/web" "$app/web"
cp "$notes/packaging/lodge/app.json" "$app/app.json"

pids=
cleanup() {
	for p in $pids; do kill "$p" 2>/dev/null || true; done
	wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# lodge's development build: the e2e identity allowed, no fake apps.
say "starting lodge's development build"
FAKE_APPS=0 LODGE_PORT="$port" LODGE_CONTENT_PORT="$cport" \
	"$lodge/dev/e2e.sh" "$root" >"$logs/lodge.log" 2>&1 &
pids="$pids $!"
wait_for() { # path, what
	i=0
	while [ ! -e "$1" ]; do
		i=$((i + 1))
		[ "$i" -le 600 ] || die "$2 did not start; see $logs"
		sleep 0.1
	done
}
wait_for "$root/run/lodge/control.sock" "lodge"

ctl() { LODGE_ROOT="$root" "$lodge/build/lodgectl" "$@"; }

# notesd as lodge's notes app, with a generated vault.
say "starting notesd with a generated vault"
LODGE_ROOT="$root" FROSTNOTES_DATA_DIR="$root/state/notes" \
	FROSTNOTES_SOCKET="$root/run/frostnotes/notesd.sock" FROSTNOTES_SECRETS=file \
	"$notes/build/notesd" -lodge >"$logs/notesd.log" 2>&1 &
pids="$pids $!"
wait_for "$root/run/frostnotes/notesd.sock" "notesd"
notesctl() { FROSTNOTES_SOCKET="$root/run/frostnotes/notesd.sock" "$notes/build/notesctl" "$@"; }

printf '%s\n' '# Garden' '' 'Rows of quince.' >"$root/vault/Garden.md"
printf '%s\n' '# Other' '' 'Pears by the wall.' >"$root/vault/Other.md"
printf '%s\n' '# Phone' '' 'Typed on the phone:' >"$root/vault/Phone.md"
notesctl vault add "$root/vault" >/dev/null || die "register the vault"
vault=$(notesctl vault list | head -n 1 | cut -f 1)
[ -n "$vault" ] || die "notesctl vault list named no vault"
say "the generated vault is vault $vault at <root>/vault"

# readd: its configuration names the vault, and its state stays under the
# root. lodge_url is not set: the development shell is plain http, and the
# note's link to the article is a rooted one, which notes follows in lodge.
cat >"$root/frostread.json" <<EOF
{
  "poll_interval": "20m",
  "notes_folder": "Reading/",
  "notes_vault": $vault
}
EOF
say "starting readd (devfetch)"
LODGE_ROOT="$root" STATE_DIRECTORY="$root/state/frostread" READ_CONFIG="$root/frostread.json" \
	READ_DEV_ALLOW="$devfeeds" \
	"$repo/build/readd" --socket "$root/run/frostread/readctl.sock" >"$logs/readd.log" 2>&1 &
pids="$pids $!"
wait_for "$root/run/frostread/readctl.sock" "readd"

# The generated feeds.
say "starting the development feeds on $devfeeds"
"$repo/build/readdev" -addr "$devfeeds" >"$logs/readdev.log" 2>&1 &
pids="$pids $!"

ctl reload >/dev/null 2>&1 || die "lodgectl reload"

# frostread's notes token: scoped to Reading/, for this user on the agent
# socket. It goes from lodgectl's stdout to readctl's stdin, and is never
# printed.
readctl() { "$repo/build/readctl" --socket "$root/run/frostread/readctl.sock" "$@"; }
say "issuing frostread's notes token"
token=$(ctl token issue frostread-notes --app notes --scope "vault=$vault;write=Reading/" \
	--agent "$(id -un)" 2>"$logs/token.log") || die "token issue failed; see $logs/token.log"
printf '%s\n' "$token" | readctl notes-token >/dev/null || die "readctl notes-token failed"
unset token

say "importing the development feeds and polling"
readctl import "$repo/testdata/dev/subscriptions.opml" >/dev/null || die "readctl import failed"
readctl poll >/dev/null || die "readctl poll failed"

cat <<EOF

lodge (development build): http://127.0.0.1:$port/
the reader:               http://127.0.0.1:$port/read/
notes:                    http://127.0.0.1:$port/notes/$vault/
root:                     $root
logs:                     $root/logs
stop:                     Ctrl-C

The first poll runs at start, and an article is ready once its extraction
has run: a second or two. Then, for example:
  $repo/build/readctl --socket $root/run/frostread/readctl.sock search tomato
EOF
[ -f "$repo/build/web/index.html" ] || cat <<EOF

frostread has no web build, so lodge disables its page; readctl, MCP and
save-as-note work. Build it with: make -C $repo web-build   # nsl
EOF
wait
