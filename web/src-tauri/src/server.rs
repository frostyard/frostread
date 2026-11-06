//! The Server sheet's mode commands (ADR-0008, plan 0002 Phase 6).
//! These commands write app.json and move readd into lodge or local mode.

use std::ffi::OsString;
use std::future::Future;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::{Duration, Instant as StdInstant};

use serde::Serialize;
use tauri::{State, Window};
use tokio::sync::Mutex;
use tokio::time::Instant;

use crate::{bridge, paths, remote};

const STOP_ERROR: &str = "This computer's reader did not stop. Frostread still uses it.";
const LOCAL_START_ERROR: &str = "Frostread now uses this computer's reader, but it did not start. See readd.log in ~/.var/app/org.frostyard.Frostread/.local/state/frostread.";

#[derive(Default)]
pub struct SaveState {
    save: Mutex<()>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ServerInfo {
    lodge: String,
    local_data: bool,
}

/// Read the Server sheet's local facts.
#[tauri::command]
pub fn server_get() -> Result<ServerInfo, String> {
    server_get_inner(|key| std::env::var_os(key))
}

fn server_get_inner(getenv: impl Fn(&str) -> Option<OsString>) -> Result<ServerInfo, String> {
    let mode = remote::load_from(&getenv)?;
    let lodge = if mode.lodge.is_empty() {
        String::new()
    } else {
        match remote::Lodge::parse(&mode.lodge) {
            Ok(lodge) => lodge.origin().to_string(),
            Err(_) => mode.lodge,
        }
    };
    let state = remote::desktop_state_dir_from(&getenv)?;
    Ok(ServerInfo {
        lodge,
        local_data: state.join("frostread.db").is_file(),
    })
}

/// Save one Server sheet change, and hold the save lock for the whole operation.
#[tauri::command]
pub async fn server_save(
    window: Window,
    bridge: State<'_, bridge::Bridge>,
    save: State<'_, SaveState>,
    lodge: String,
) -> Result<(), String> {
    let _save = save.save.lock().await;
    let label = window.label().to_string();
    let bridge_ref = bridge.inner();

    server_save_inner(
        |key| std::env::var_os(key),
        Instant::now() + Duration::from_secs(30),
        lodge,
        |deadline, lock, socket| async move {
            bridge::stop_local_readd_inner(deadline, &lock, &socket).await
        },
        || async { start_local_readd_inner().await },
        || async {
            bridge_ref.forget(&label).await;
        },
    )
    .await
}

/// One Server sheet save, with the environment and process actions provided.
/// It has no Tauri Window, so each mode order is testable by itself.
async fn server_save_inner<FStop, S, FStart, T, FForget, F>(
    getenv: impl Fn(&str) -> Option<OsString>,
    deadline: Instant,
    lodge: String,
    stop_local: FStop,
    start_local: FStart,
    forget: FForget,
) -> Result<(), String>
where
    FStop: FnOnce(Instant, PathBuf, PathBuf) -> S,
    S: Future<Output = Result<(), String>>,
    FStart: FnOnce() -> T,
    T: Future<Output = Result<(), String>>,
    FForget: FnOnce() -> F,
    F: Future<Output = ()>,
{
    let mode_path = remote::mode_path_from(&getenv)?;
    let requested = lodge.trim();

    if requested.is_empty() {
        write_mode(&mode_path, None)?;
        forget().await;
        if start_local().await.is_err() {
            return Err(LOCAL_START_ERROR.to_string());
        }
        return Ok(());
    }

    let lodge = remote::Lodge::parse(requested)?;
    let state = remote::desktop_state_dir_from(&getenv)?;
    let socket = paths::socket_from(&getenv)?;

    write_mode(&mode_path, Some(lodge.origin()))?;

    let lock = state.join("readd.lock");
    if let Err(_stop_error) = stop_local(deadline, lock, socket).await {
        if let Err(write_error) = write_mode(&mode_path, None) {
            return Err(format!(
                "This computer's reader did not stop, and Frostread could not write local back to app.json: {write_error}"
            ));
        }
        return Err(STOP_ERROR.to_string());
    }

    forget().await;
    Ok(())
}

/// Start readd through the Flatpak launcher. Missing, failed and timed-out
/// starts all become the Server sheet's local-start error.
async fn start_local_readd_inner() -> Result<(), String> {
    tokio::task::spawn_blocking(|| {
        let mut child = std::process::Command::new("frostread")
            .arg("--start")
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .map_err(|_| LOCAL_START_ERROR.to_string())?;
        let deadline = StdInstant::now() + Duration::from_secs(30);

        loop {
            match child.try_wait() {
                Ok(Some(status)) if status.success() => return Ok(()),
                Ok(Some(_)) | Err(_) => return Err(LOCAL_START_ERROR.to_string()),
                Ok(None) if StdInstant::now() >= deadline => {
                    let _ = child.kill();
                    let _ = child.wait();
                    return Err(LOCAL_START_ERROR.to_string());
                }
                Ok(None) => std::thread::sleep(Duration::from_millis(50)),
            }
        }
    })
    .await
    .unwrap_or_else(|_| Err(LOCAL_START_ERROR.to_string()))
}

/// Set or remove lodge in app.json without losing other object members.
/// The replacement is temporary, synced, renamed, and directory-synced.
pub(crate) fn write_mode(path: &Path, lodge: Option<&str>) -> Result<(), String> {
    let dir = path
        .parent()
        .ok_or_else(|| format!("mode path {} has no directory", path.display()))?;

    if !dir.is_dir() {
        let mut builder = std::fs::DirBuilder::new();
        builder.recursive(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::DirBuilderExt;
            builder.mode(0o700);
        }
        builder
            .create(dir)
            .map_err(|e| format!("create {}: {e}", dir.display()))?;
    }

    let mut object = match std::fs::read(path) {
        Ok(data) => match serde_json::from_slice::<serde_json::Value>(&data) {
            Ok(serde_json::Value::Object(object)) => object,
            _ => serde_json::Map::new(),
        },
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => serde_json::Map::new(),
        Err(e) => return Err(format!("read {}: {e}", path.display())),
    };

    match lodge {
        Some(lodge) => {
            object.insert(
                "lodge".to_string(),
                serde_json::Value::String(lodge.to_string()),
            );
        }
        None => {
            object.remove("lodge");
        }
    }

    let bytes = serde_json::to_vec(&serde_json::Value::Object(object))
        .map_err(|e| format!("encode {}: {e}", path.display()))?;

    static NEXT: AtomicUsize = AtomicUsize::new(0);
    let temp = dir.join(format!(
        ".app.json.tmp-{}-{}",
        std::process::id(),
        NEXT.fetch_add(1, Ordering::Relaxed)
    ));

    let result = (|| -> Result<(), String> {
        let mut file = {
            #[cfg(unix)]
            {
                use std::os::unix::fs::OpenOptionsExt;
                let mut options = std::fs::OpenOptions::new();
                options.write(true).create_new(true).mode(0o600);
                options
                    .open(&temp)
                    .map_err(|e| format!("create {}: {e}", temp.display()))?
            }
            #[cfg(not(unix))]
            {
                std::fs::OpenOptions::new()
                    .write(true)
                    .create_new(true)
                    .open(&temp)
                    .map_err(|e| format!("create {}: {e}", temp.display()))?
            }
        };
        file.write_all(&bytes)
            .map_err(|e| format!("write {}: {e}", temp.display()))?;
        file.sync_all()
            .map_err(|e| format!("sync {}: {e}", temp.display()))?;
        drop(file);
        std::fs::rename(&temp, path)
            .map_err(|e| format!("rename {} to {}: {e}", temp.display(), path.display()))?;
        let directory =
            std::fs::File::open(dir).map_err(|e| format!("open {}: {e}", dir.display()))?;
        directory
            .sync_all()
            .map_err(|e| format!("sync {}: {e}", dir.display()))?;
        Ok(())
    })();

    if result.is_err() {
        let _ = std::fs::remove_file(&temp);
    }
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;
    use std::sync::atomic::{AtomicUsize, Ordering};

    struct TempGuard(std::path::PathBuf);

    impl TempGuard {
        fn new(label: &str) -> Self {
            static NEXT: AtomicUsize = AtomicUsize::new(0);
            let path = std::env::temp_dir().join(format!(
                "frostread-server-{label}-{}-{}",
                std::process::id(),
                NEXT.fetch_add(1, Ordering::Relaxed)
            ));
            let _ = std::fs::remove_dir_all(&path);
            std::fs::create_dir_all(&path).unwrap();
            TempGuard(path)
        }

        fn path(&self) -> &std::path::Path {
            &self.0
        }
    }

    impl Drop for TempGuard {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.0);
        }
    }

    fn env(dir: &std::path::Path) -> impl Fn(&str) -> Option<OsString> + '_ {
        move |key| match key {
            "XDG_CONFIG_HOME" => Some(OsString::from(dir.join("config"))),
            "XDG_DATA_HOME" => Some(OsString::from(dir.join("data"))),
            "XDG_RUNTIME_DIR" => Some(OsString::from(dir.join("run"))),
            "HOME" => Some(OsString::from(dir.join("home"))),
            _ => None,
        }
    }

    fn block_on<F: Future>(future: F) -> F::Output {
        tauri::async_runtime::block_on(future)
    }

    fn read_lodge(path: &Path) -> serde_json::Value {
        serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap()
    }

    #[test]
    fn write_mode_keeps_unknown_sets_removes_and_sets_permissions() {
        let guard = TempGuard::new("write-mode");
        let dir = guard.path();
        let config = dir.join("config/frostread");
        std::fs::create_dir_all(&config).unwrap();
        std::fs::set_permissions(&config, std::fs::Permissions::from_mode(0o700)).unwrap();
        let path = config.join("app.json");
        std::fs::write(&path, r#"{"theme":"dark","count":3}"#).unwrap();

        write_mode(&path, Some("https://lodge.example.ts.net")).unwrap();
        let value = read_lodge(&path);
        assert_eq!(value["theme"], "dark");
        assert_eq!(value["count"], 3);
        assert_eq!(value["lodge"], "https://lodge.example.ts.net");

        let getenv = env(dir);
        let loaded = remote::load_from(&getenv).unwrap();
        assert_eq!(loaded.lodge, "https://lodge.example.ts.net");

        write_mode(&path, None).unwrap();
        let value = read_lodge(&path);
        assert_eq!(value["theme"], "dark");
        assert!(value.get("lodge").is_none());

        let mode_dir = dir.join("config/frostread");
        assert_eq!(
            mode_dir.metadata().unwrap().permissions().mode() & 0o777,
            0o700
        );
        assert_eq!(path.metadata().unwrap().permissions().mode() & 0o777, 0o600);

        let new_guard = TempGuard::new("write-mode-new-dir");
        let new_path = new_guard.path().join("missing/config/frostread/app.json");
        write_mode(&new_path, Some("https://lodge.example.ts.net")).unwrap();
        assert_eq!(
            new_path
                .parent()
                .unwrap()
                .metadata()
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o700
        );
        assert_eq!(
            new_path.metadata().unwrap().permissions().mode() & 0o777,
            0o600
        );
    }

    #[test]
    fn save_lodge_stops_after_writing_lodge_and_never_starts() {
        let guard = TempGuard::new("save-lodge");
        let dir = guard.path();
        let mode = dir.join("config/frostread/app.json");
        std::fs::create_dir_all(mode.parent().unwrap()).unwrap();
        let getenv = env(dir);

        block_on(server_save_inner(
            getenv,
            Instant::now() + Duration::from_secs(30),
            "https://lodge.example.ts.net".to_string(),
            move |_, lock, socket| async move {
                assert_eq!(read_lodge(&mode)["lodge"], "https://lodge.example.ts.net");
                assert_eq!(lock.file_name().unwrap(), "readd.lock");
                assert!(socket.ends_with("readd.sock"));
                Ok(())
            },
            || async { panic!("start must not run for lodge") },
            || async {},
        ))
        .unwrap();
        assert_eq!(
            read_lodge(&dir.join("config/frostread/app.json"))["lodge"],
            "https://lodge.example.ts.net"
        );
    }

    #[test]
    fn save_lodge_stop_failure_writes_local_exact_error() {
        let guard = TempGuard::new("save-lodge-stop-fail");
        let dir = guard.path();
        let mode = dir.join("config/frostread/app.json");
        std::fs::create_dir_all(mode.parent().unwrap()).unwrap();
        let getenv = env(dir);

        let error = block_on(server_save_inner(
            getenv,
            Instant::now() + Duration::from_secs(30),
            "https://lodge.example.ts.net".to_string(),
            |_, _, _| async { Err("stop failed".to_string()) },
            || async { panic!("start must not run for lodge") },
            || async {},
        ))
        .unwrap_err();
        assert_eq!(error, STOP_ERROR);
        assert!(read_lodge(&mode).get("lodge").is_none());
    }

    #[test]
    fn real_lock_stop_fails_and_save_backwrites_local() {
        let guard = TempGuard::new("save-real-lock");
        let dir = guard.path();
        let getenv = env(dir);
        let state = remote::desktop_state_dir_from(&getenv).unwrap();
        std::fs::create_dir_all(&state).unwrap();
        let lock_path = state.join("readd.lock");
        let lock = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .open(&lock_path)
            .unwrap();
        lock.try_lock().unwrap();

        let mode = remote::mode_path_from(&getenv).unwrap();
        let error = block_on(server_save_inner(
            &getenv,
            Instant::now() + Duration::from_millis(50),
            "https://lodge.example.ts.net".to_string(),
            |deadline, lock, socket| async move {
                bridge::stop_local_readd_inner(deadline, &lock, &socket).await
            },
            || async { panic!("start must not run for lodge") },
            || async {},
        ))
        .unwrap_err();
        assert_eq!(error, STOP_ERROR);
        assert!(read_lodge(&mode).get("lodge").is_none());

        lock.unlock().unwrap();
    }

    #[test]
    fn save_local_removes_lodge_before_start_and_keeps_exact_failure_text() {
        let guard = TempGuard::new("save-local");
        let dir = guard.path();
        let mode = dir.join("config/frostread/app.json");
        std::fs::create_dir_all(mode.parent().unwrap()).unwrap();
        write_mode(&mode, Some("https://lodge.example.ts.net")).unwrap();
        let getenv = env(dir);
        let mode_for_start = mode.clone();

        let error = block_on(server_save_inner(
            getenv,
            Instant::now() + Duration::from_secs(30),
            "".to_string(),
            |_, _, _| async { panic!("stop must not run for local") },
            move || {
                let mode = mode_for_start.clone();
                async move {
                    assert!(read_lodge(&mode).get("lodge").is_none());
                    Err("missing launcher".to_string())
                }
            },
            || async {},
        ))
        .unwrap_err();
        assert_eq!(error, LOCAL_START_ERROR);
        assert!(read_lodge(&mode).get("lodge").is_none());
    }

    #[test]
    fn bad_lodge_address_writes_nothing() {
        let guard = TempGuard::new("bad-lodge");
        let dir = guard.path();
        let mode = dir.join("config/frostread/app.json");
        std::fs::create_dir_all(mode.parent().unwrap()).unwrap();
        std::fs::write(
            &mode,
            r#"{"theme":"dark","lodge":"https://old.example.ts.net"}"#,
        )
        .unwrap();
        let before = std::fs::read(&mode).unwrap();

        let result = block_on(server_save_inner(
            env(dir),
            Instant::now() + Duration::from_secs(30),
            "http://lodge.example.ts.net".to_string(),
            |_, _, _| async { Ok(()) },
            || async { Ok(()) },
            || async {},
        ));
        assert!(result.is_err());
        assert_eq!(std::fs::read(&mode).unwrap(), before);
    }

    #[test]
    fn server_get_reports_local_data_and_camel_case_json() {
        let guard = TempGuard::new("server-get");
        let dir = guard.path();
        let getenv = env(dir);
        let mode = remote::mode_path_from(&getenv).unwrap();

        std::fs::create_dir_all(mode.parent().unwrap()).unwrap();
        std::fs::write(&mode, r#"{"lodge":"https://lodge.example.ts.net"}"#).unwrap();
        let info = server_get_inner(&getenv).unwrap();
        assert_eq!(info.lodge, "https://lodge.example.ts.net");
        assert!(!info.local_data);
        let json = serde_json::to_value(&info).unwrap();
        assert_eq!(json["lodge"], "https://lodge.example.ts.net");
        assert_eq!(json["localData"], false);

        let state = remote::desktop_state_dir_from(&getenv).unwrap();
        std::fs::create_dir_all(&state).unwrap();
        std::fs::write(state.join("frostread.db"), []).unwrap();
        let info = server_get_inner(&getenv).unwrap();
        assert!(info.local_data);

        std::fs::write(&mode, r#"{"lodge":"lodge.example.ts.net"}"#).unwrap();
        let info = server_get_inner(&getenv).unwrap();
        assert_eq!(info.lodge, "lodge.example.ts.net");

        std::fs::write(&mode, r#"{"bad""#).unwrap();
        assert!(server_get_inner(&getenv).is_err());
    }

    #[test]
    fn grants_only_the_shell_commands() {
        let caps = include_str!("../capabilities/default.json");
        let list = caps
            .split("\"permissions\"")
            .nth(1)
            .and_then(|s| s.split(']').next())
            .unwrap();
        let granted: Vec<&str> = list.split('"').skip(1).step_by(2).collect();
        assert_eq!(
            granted,
            [
                "allow-readd-connect",
                "allow-readd-send",
                "allow-save-file",
                "allow-server-get",
                "allow-server-save"
            ]
        );
    }
}
