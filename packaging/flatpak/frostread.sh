#!/bin/sh
# Frostread's launcher in the Flatpak, for now
# (docs/adr/0008-the-desktop-flatpak-carries-its-own-readd-and-lodge-is-a-connection.md).
# The manifest installs it as /app/bin/frostread, the manifest's command,
# and the Tauri app as /app/libexec/frostread-app.
#
# This interim form only runs the app: it starts no readd, so the window
# says the reader is not running unless one already runs. The next part of
# plan 0002's Phase 4 replaces it with ADR-0008's launcher: `--background`,
# the start through `flatpak-spawn --latest-version`, and the Background
# request.
exec /app/libexec/frostread-app "$@"
