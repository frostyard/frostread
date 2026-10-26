#!/bin/sh
# Stage readd as lodge's read app under DESTDIR, in lodge's install
# layout (lodge's app-contract.md, Install layout): the daemon, readctl, the
# manifest with VERSION, the web build when BUILD/web holds one, the units
# and the sysusers.d entry. make lodge-package runs it.
#
#   packaging/lodge/stage.sh DESTDIR VERSION [BUILD]
#
# DESTDIR may be a lodge root that other apps and lodge itself share, so
# this removes nothing of theirs: only the app's own directory is replaced,
# so a web build's stale files go, and every other file it stages is
# overwritten in place. DESTDIR must not be empty or /. It writes only under
# DESTDIR/usr, so a sysext can carry the result (frostyard/core ADR-0058).
set -eu

usage="usage: packaging/lodge/stage.sh DESTDIR VERSION [BUILD]"
dest=${1:?$usage}
version=${2:?$usage}
build=${3:-build}
here=$(cd "$(dirname "$0")" && pwd)

if [ "$(realpath -m -- "$dest")" = / ]; then
  echo "stage.sh: refusing to stage into /; install from a staged root instead" >&2
  exit 1
fi
case $version in
  *[!A-Za-z0-9._+-]*) echo "stage.sh: version $version has characters a manifest cannot carry" >&2; exit 1 ;;
esac

app=$dest/usr/share/lodge/apps/read
rm -rf -- "$app"
install -Dm755 "$build/readd" "$dest/usr/bin/readd"
install -Dm755 "$build/readctl" "$dest/usr/bin/readctl"
install -d "$app"
sed "s/\"version\": \"[^\"]*\"/\"version\": \"$version\"/" "$here/app.json" > "$app/app.json"
if [ -d "$build/web" ]; then cp -R "$build/web" "$app/web"; fi
install -Dm644 "$here/frostread.socket" "$dest/usr/lib/systemd/system/frostread.socket"
install -Dm644 "$here/frostread.service" "$dest/usr/lib/systemd/system/frostread.service"
install -Dm644 "$here/sysusers.d-frostread.conf" "$dest/usr/lib/sysusers.d/frostread.conf"
