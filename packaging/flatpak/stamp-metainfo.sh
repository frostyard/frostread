#!/bin/sh
# Add a release to the Frostread Flatpak metainfo on a v* tag run.
set -eu

die() {
	printf 'stamp-metainfo.sh: %s\n' "$*" >&2
	exit 1
}

[ "$#" -ge 1 ] && [ "$#" -le 2 ] || die 'usage: stamp-metainfo.sh VERSION [METINFO]'

version=$1
metainfo=${2:-$(dirname "$0")/org.frostyard.Frostread.metainfo.xml}

printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' ||
	die "version '$version' is not MAJOR.MINOR.PATCH"
[ -f "$metainfo" ] || die "no metainfo at $metainfo"

found=$(grep -oF '<releases>' "$metainfo" | wc -l)
[ "$found" -eq 1 ] || die "$metainfo holds <releases> $found times, want exactly once"
if grep -Fq "version=\"$version\"" "$metainfo"; then
	die "$metainfo already lists version $version"
fi

# Every check above comes before the first edit.
today=$(date -u +%Y-%m-%d)
release="<release version=\"$version\" date=\"$today\"/>"
indent=$(sed -n 's/^\([[:space:]]*\)<release[[:space:]].*$/\1/p' "$metainfo" | head -n 1)
if [ -z "$indent" ]; then
	base=$(sed -n 's/^\([[:space:]]*\)<releases>.*$/\1/p' "$metainfo")
	indent=$base'  '
fi
sed -i "/<releases>/a\\
$indent$release" "$metainfo"
grep -Fq -- "$release" "$metainfo" || die "$metainfo did not take the release"

printf 'stamped %s into %s\n' "$version" "$metainfo"
