#!/bin/sh
# Check a .deb that make deb built (frostyard/core ADR-0011, ADR-0058):
#
#   packaging/deb-check.sh DEB PACKAGE VERSION
#
# It fails unless the package's fields are right, every path is /usr or under
# it, every entry is root's and none is group- or world-writable, and the
# payload is there: the binaries, the units, the sysusers.d entry, and the
# app's manifest (whose version is VERSION) and web bundle. It prints
# dpkg-deb -c so a CI log shows the contents.
set -eu

usage="usage: packaging/deb-check.sh DEB PACKAGE VERSION"
deb=${1:?$usage}
pkg=${2:?$usage}
version=${3:?$usage}
fail=0
bad() { echo "deb-check: $*" >&2; fail=1; }

[ -f "$deb" ] || { echo "deb-check: $deb does not exist (run make deb)" >&2; exit 1; }

field() { dpkg-deb -f "$deb" "$1"; }
[ "$(field Package)" = "$pkg" ] || bad "Package is $(field Package), want $pkg"
[ "$(field Version)" = "$version" ] || bad "Version is $(field Version), want $version"
[ "$(field Architecture)" = amd64 ] || bad "Architecture is $(field Architecture), want amd64"

list=$(dpkg-deb -c "$deb")
echo "$list"

# One entry per line: mode owner/group size date time path.
echo "$list" | awk '
  $6 != "./usr/" && index($6, "./usr/") != 1 { print "path outside /usr: " $6; bad = 1 }
  $2 != "root/root" { print "not root/root: " $6 " (" $2 ")"; bad = 1 }
  substr($1, 6, 1) == "w" || substr($1, 9, 1) == "w" { print "group- or world-writable: " $6 " (" $1 ")"; bad = 1 }
  END { exit bad }
' >&2 || fail=1

# Every expected file, as a regular file.
for f in \
  ./usr/bin/readd \
  ./usr/bin/readctl \
  ./usr/lib/systemd/system/frostread.socket \
  ./usr/lib/systemd/system/frostread.service \
  ./usr/lib/sysusers.d/frostread.conf \
  ./usr/share/lodge/apps/read/app.json \
  ./usr/share/lodge/apps/read/web/index.html; do
  echo "$list" | awk -v f="$f" '$6 == f && substr($1, 1, 1) == "-" { found = 1 } END { exit !found }' ||
    bad "missing file $f"
done

manifest=$(dpkg-deb --fsys-tarfile "$deb" | tar -xOf - ./usr/share/lodge/apps/read/app.json 2>/dev/null || true)
echo "$manifest" | grep -q "\"version\": \"$version\"" || bad "app.json's version is not $version"

[ "$fail" = 0 ] || exit 1
echo "deb-check: $deb ok"
