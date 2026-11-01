//! The shell's build: Tauri's, and the app's own commands in a manifest, so
//! that each one needs a permission in capabilities/default.json. Nothing
//! of Tauri's is granted there, so a command the page gains reaches it only
//! on purpose.

fn main() {
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(manifest()))
        .expect("frostread's build failed");
}

/// The app's own commands: the bridge's two, and nothing else.
fn manifest() -> tauri_build::AppManifest {
    tauri_build::AppManifest::new().commands(&["readd_connect", "readd_send"])
}
