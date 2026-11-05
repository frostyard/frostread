#!/bin/sh
# Print the release version of HEAD for the Flatpak's build
# (docs/specs/problem-records.md, Version stamping): X.Y.Z or X.Y.Z-rc.N,
# without the "v", when HEAD has exactly a tag vX.Y.Z or vX.Y.Z-rc.N.
# Print nothing otherwise: an untagged commit, a tag of another shape, or
# no git at all. It always exits 0, and it reaches no network.
#
#   sh scripts/release-version.sh > packaging/flatpak/release-version
tags=$(git tag --points-at HEAD 2>/dev/null) || exit 0
for tag in $tags; do
    if printf '%s\n' "$tag" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$'; then
        printf '%s\n' "${tag#v}"
        exit 0
    fi
done
exit 0
