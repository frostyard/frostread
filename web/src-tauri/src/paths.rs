//! readd's socket, as internal/config.DesktopSocket derives it
//! (ADR-0008, readd's desktop mode). One table of cases,
//! internal/config/testdata/desktop-socket.json, is read by a Go test and
//! by a Rust test, so the two derivations cannot drift.

use std::ffi::OsString;
use std::path::{Component, Path, PathBuf};

/// The application ID, as internal/config.AppID.
const APP_ID: &str = "org.frostyard.Frostread";

/// readd's private socket for this process.
pub fn socket() -> Result<PathBuf, String> {
    socket_from(|key| std::env::var_os(key))
}

/// socket_from is DesktopSocket: READ_SOCKET when it is not empty (whatever
/// it holds), else an absolute XDG_RUNTIME_DIR and frostread/readd.sock
/// under it, in the directory the Flatpak's sandboxes share when FLATPAK_ID
/// is the app's. Flatpak shares $XDG_RUNTIME_DIR/app/<id> between every
/// sandbox of the app, and the rest of $XDG_RUNTIME_DIR is private to each
/// one, so the window and readd meet only there.
fn socket_from(getenv: impl Fn(&str) -> Option<OsString>) -> Result<PathBuf, String> {
    if let Some(over) = getenv("READ_SOCKET").filter(|value| !value.is_empty()) {
        return Ok(PathBuf::from(over));
    }
    let runtime = getenv("XDG_RUNTIME_DIR")
        .map(PathBuf::from)
        .filter(|path| path.is_absolute());
    let Some(mut runtime) = runtime else {
        return Err("XDG_RUNTIME_DIR is not set; set READ_SOCKET".to_string());
    };
    if getenv("FLATPAK_ID").is_some_and(|id| id == APP_ID) {
        runtime.push("app");
        runtime.push(APP_ID);
    }
    runtime.push("frostread");
    runtime.push("readd.sock");
    // Go's filepath.Join cleans the path it makes; READ_SOCKET stays as it
    // was given, as in DesktopSocket.
    Ok(clean(&runtime))
}

/// clean is filepath.Clean for an absolute path, as filepath.Join applies
/// it: a `.` element and repeated or trailing separators go, and `..` goes
/// up one element but never above the root.
fn clean(path: &Path) -> PathBuf {
    let mut clean = PathBuf::new();
    for part in path.components() {
        match part {
            Component::RootDir => clean.push("/"),
            Component::CurDir => {}
            // pop leaves the root alone, so ".." never goes above it.
            Component::ParentDir => {
                clean.pop();
            }
            Component::Normal(name) => clean.push(name),
            Component::Prefix(_) => {}
        }
    }
    clean
}

#[cfg(test)]
mod tests {
    use super::socket_from;
    use serde::Deserialize;
    use std::collections::HashMap;
    use std::ffi::OsString;
    use std::path::PathBuf;

    /// The table's shape, as internal/config's Go test reads it.
    #[derive(Deserialize)]
    struct Case {
        name: String,
        env: HashMap<String, String>,
        #[serde(default)]
        socket: String,
        #[serde(default)]
        error: bool,
    }

    /// The table Go and Rust share: each case's environment gives its
    /// socket, or its error.
    #[test]
    fn the_shared_table_holds() {
        let json = include_str!("../../../internal/config/testdata/desktop-socket.json");
        let table: Vec<Case> = serde_json::from_str(json).expect("the shared table's JSON");
        assert!(
            table.len() >= 8,
            "the table holds {} cases; want at least 8",
            table.len()
        );
        for case in table {
            let env = |key: &str| {
                case.env
                    .get(key)
                    .map(|value| OsString::from(value.as_str()))
            };
            let got = socket_from(env);
            if case.error {
                let Err(message) = got else {
                    panic!("{}: {got:?}; want an error", case.name)
                };
                assert!(
                    message.contains("READ_SOCKET"),
                    "{}: the error does not say to set READ_SOCKET: {message}",
                    case.name
                );
            } else {
                assert_eq!(got, Ok(PathBuf::from(&case.socket)), "{}", case.name);
            }
        }
    }
}
