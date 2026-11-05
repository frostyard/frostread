#!/bin/sh
# Build the Tauri shell in the Flatpak's frostread module, with the release
# version that readd carries (docs/specs/problem-records.md, Version
# stamping). The readd module builds first and stamps readd with
# packaging/flatpak/release-version, or with "dev". For a release version,
# X.Y.Z or X.Y.Z-rc.N, the shell gets the same version, and its problem
# records carry it; for any other, the shell keeps tauri.conf.json's 0.0.0,
# and its recorder is inert. READD names another readd, for a test.
set -eu
version=$("${READD:-/app/bin/readd}" -version)
if printf '%s\n' "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$'; then
    cd web && exec pnpm tauri build --config "{\"version\":\"$version\"}"
fi
cd web && exec pnpm tauri build
