//! The shell's problem records: its panics, in the local systemd journal,
//! with frostyard's `frostyard-problems` crate. The shell sends nothing over
//! a network (docs/specs/problem-records.md).

use frostyard_problems::{Component, Recorder};

/// The app ID of the shell's records: the Flatpak's ID.
pub const APP_ID: &str = "org.frostyard.Frostread";

/// The version an unstamped build carries: tauri.conf.json's.
const UNSTAMPED: &str = "0.0.0";

/// recorder is the shell's recorder (component `gui`) at `version`, the
/// version Tauri's context gives. A release build carries its tag's
/// version (packaging/flatpak/tauri-build.sh), and records. An unstamped
/// build carries tauri.conf.json's `0.0.0`, which is a valid semver: it
/// becomes `0.0.0-unstamped`, which the crate's release pattern refuses
/// (a pre-release must be alpha, beta or rc with a number), so that
/// recorder is inert.
pub fn recorder(version: &str) -> Recorder {
    let version = if version == UNSTAMPED {
        "0.0.0-unstamped"
    } else {
        version
    };
    Recorder::new(APP_ID, version, Component::Gui)
}

#[cfg(test)]
mod tests {
    use super::{APP_ID, recorder};

    #[test]
    fn an_unstamped_build_records_nothing() {
        assert!(recorder("0.0.0").err().is_some());
    }

    #[test]
    fn a_release_records() {
        for version in ["1.2.3", "1.2.3-rc.1"] {
            assert_eq!(recorder(version).err(), None, "{version}");
        }
    }

    // A panic writes 1 valid record to the journal socket, with the app's
    // ID, version and component, and the panic's location in this crate.
    #[cfg(target_os = "linux")]
    #[test]
    fn a_panic_is_recorded() {
        use std::os::unix::net::UnixDatagram;
        use std::time::Duration;

        // A short folder: a socket's path is under 108 bytes.
        let dir = std::env::temp_dir().join(format!("frp-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let socket = dir.join("s");
        let _ = std::fs::remove_file(&socket);
        assert!(socket.as_os_str().len() < 108, "{}", socket.display());
        let journal = UnixDatagram::bind(&socket).unwrap();
        journal
            .set_read_timeout(Some(Duration::from_secs(10)))
            .unwrap();

        let rec = recorder("1.2.3").with_socket(&socket);
        rec.install_panic_hook(env!("CARGO_MANIFEST_DIR"));
        let line = line!() + 1;
        let caught = std::panic::catch_unwind(|| panic!("a shell with a bug"));
        // The hook is the test's alone: put the default one back.
        let _ = std::panic::take_hook();
        assert!(caught.is_err());

        let mut buf = vec![0u8; 1 << 16];
        let n = journal.recv(&mut buf).expect("no problem record");
        let _ = std::fs::remove_dir_all(&dir);
        let fields = frostyard_problems::decode_entry(&buf[..n]).unwrap();
        let detail = frostyard_problems::validate(&fields).unwrap();
        assert_eq!(detail.app_id, APP_ID);
        assert_eq!(detail.app_version, "1.2.3");
        assert_eq!(detail.runtime, "rust");
        assert_eq!(detail.component, "gui");
        assert_eq!(detail.class, "panic");
        assert_eq!(detail.code, None);
        assert_eq!(
            fields.get("MESSAGE").map(String::as_str),
            Some("a shell with a bug")
        );
        let location = detail.location.expect("no location");
        assert!(location.starts_with("src/"), "{location}");
        assert!(location.ends_with(&format!(".rs:{line}")), "{location}");
    }
}
