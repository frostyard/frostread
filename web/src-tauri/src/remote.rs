//! A lodge address and the desktop app's mode file (ADR-0008).
//! The Rust derivations copy internal/config: a shared test table keeps
//! the XDG paths, and the mode's JSON rules, in step.

use std::collections::HashSet;
use std::ffi::OsString;
use std::path::PathBuf;

use crate::paths;
use serde::{
    Deserialize, Deserializer,
    de::{IgnoredAny, MapAccess, Visitor},
};
use tauri::Url;

const MODE_FILE: &str = "app.json";

/// The lodge value from app.json. An absent or empty value is local mode.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Mode {
    pub lodge: String,
}

struct RawMode {
    lodge: Option<String>,
}

impl<'de> Deserialize<'de> for RawMode {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        struct RawModeVisitor;

        impl<'de> Visitor<'de> for RawModeVisitor {
            type Value = RawMode;

            fn expecting(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                formatter.write_str("a JSON object")
            }

            fn visit_unit<E>(self) -> Result<Self::Value, E>
            where
                E: serde::de::Error,
            {
                Ok(RawMode { lodge: None })
            }

            fn visit_none<E>(self) -> Result<Self::Value, E>
            where
                E: serde::de::Error,
            {
                Ok(RawMode { lodge: None })
            }

            fn visit_map<A>(self, mut map: A) -> Result<Self::Value, A::Error>
            where
                A: MapAccess<'de>,
            {
                let mut lodge = None;
                let mut saw_lodge = false;
                while let Some(key) = map.next_key::<String>()? {
                    if key == "lodge" {
                        if saw_lodge {
                            return Err(serde::de::Error::custom(
                                "duplicate object member \"lodge\"",
                            ));
                        }
                        saw_lodge = true;
                        lodge = map.next_value::<Option<String>>()?;
                    } else {
                        let _: IgnoredAny = map.next_value()?;
                    }
                }
                Ok(RawMode { lodge })
            }
        }

        deserializer.deserialize_any(RawModeVisitor)
    }
}

enum Frame {
    Object {
        names: HashSet<String>,
        expect_key: bool,
    },
    Array,
}

/// Check duplicate names after typed parsing. The byte scan skips values without
/// converting numbers, like Go's json/v2 when it ignores an unknown member.
pub(crate) fn reject_duplicate_names(data: &[u8]) -> Result<(), String> {
    let mut frames: Vec<Frame> = Vec::new();
    let mut i = 0;
    while i < data.len() {
        match data[i] {
            b'{' => {
                frames.push(Frame::Object {
                    names: HashSet::new(),
                    expect_key: true,
                });
                i += 1;
            }
            b'[' => {
                frames.push(Frame::Array);
                i += 1;
            }
            b'}' | b']' => {
                frames.pop();
                i += 1;
            }
            b':' => {
                if let Some(Frame::Object { expect_key, .. }) = frames.last_mut() {
                    *expect_key = false;
                }
                i += 1;
            }
            b',' => {
                if let Some(Frame::Object { expect_key, .. }) = frames.last_mut() {
                    *expect_key = true;
                }
                i += 1;
            }
            b'"' => {
                let start = i;
                i += 1;
                let mut backslashes = 0;
                while i < data.len() {
                    match data[i] {
                        b'\\' => {
                            backslashes += 1;
                            i += 1;
                        }
                        b'"' if backslashes % 2 == 0 => {
                            i += 1;
                            break;
                        }
                        b'"' => {
                            backslashes = 0;
                            i += 1;
                        }
                        _ => {
                            backslashes = 0;
                            i += 1;
                        }
                    }
                }

                if let Some(Frame::Object { names, expect_key }) = frames.last_mut()
                    && *expect_key
                {
                    let token = std::str::from_utf8(&data[start..i])
                        .map_err(|e| format!("app.json: {e}"))?;
                    let name: String =
                        serde_json::from_str(token).map_err(|e| format!("app.json: {e}"))?;
                    if !names.insert(name.clone()) {
                        return Err(format!("duplicate member name {name}"));
                    }
                    *expect_key = false;
                }
            }
            _ => i += 1,
        }
    }
    Ok(())
}

/// Read app.json beside frostread's desktop configuration.
pub(crate) fn load_from(getenv: impl Fn(&str) -> Option<OsString>) -> Result<Mode, String> {
    let path = mode_path_from(&getenv)?;
    let data = match std::fs::read(&path) {
        Ok(data) => data,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(Mode::default()),
        Err(e) => return Err(format!("read {}: {e}", path.display())),
    };
    let raw: RawMode =
        serde_json::from_slice(&data).map_err(|e| format!("{}: {e}", path.display()))?;
    reject_duplicate_names(&data).map_err(|e| format!("{}: {e}", path.display()))?;
    Ok(Mode {
        lodge: raw.lodge.unwrap_or_default().trim().to_string(),
    })
}

pub(crate) fn desktop_state_dir_from(
    getenv: impl Fn(&str) -> Option<OsString>,
) -> Result<PathBuf, String> {
    let dir = xdg_from(&getenv, "XDG_DATA_HOME", ".local/share")?;
    Ok(paths::clean(&dir.join("frostread")))
}

pub(crate) fn mode_path_from(getenv: impl Fn(&str) -> Option<OsString>) -> Result<PathBuf, String> {
    let dir = xdg_from(&getenv, "XDG_CONFIG_HOME", ".config")?;
    Ok(paths::clean(&dir.join("frostread").join(MODE_FILE)))
}

fn xdg_from(
    getenv: impl Fn(&str) -> Option<OsString>,
    name: &str,
    fallback: &str,
) -> Result<PathBuf, String> {
    if let Some(value) = getenv(name) {
        let path = PathBuf::from(value);
        if path.is_absolute() {
            return Ok(path);
        }
    }
    let home = getenv("HOME")
        .map(PathBuf::from)
        .filter(|path| path.is_absolute())
        .ok_or_else(|| "HOME is not set".to_string())?;
    Ok(paths::clean(&home.join(fallback)))
}

/// Lodge's address, with the origin and WebSocket URL made from it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Lodge {
    origin: String,
    secure: bool,
}

impl Lodge {
    pub fn parse(s: &str) -> Result<Lodge, String> {
        let url = Url::parse(s.trim()).map_err(|e| format!("lodge's address {s:?}: {e}"))?;
        let host = url
            .host_str()
            .ok_or_else(|| format!("lodge's address {s:?} has no host"))?;
        if !url.username().is_empty()
            || url.password().is_some()
            || url.query().is_some()
            || url.fragment().is_some()
        {
            return Err(format!("lodge's address {s:?} must be a scheme and a host"));
        }
        if url.path() != "/" && !url.path().is_empty() {
            return Err(format!("lodge's address {s:?} must have no path"));
        }
        let secure = match url.scheme() {
            "https" => true,
            "http" if loopback(host) => false,
            "http" => {
                return Err(format!(
                    "lodge's address {s:?} must be https off this machine"
                ));
            }
            scheme => {
                return Err(format!("lodge's address {s:?} must be https, not {scheme}"));
            }
        };
        Ok(Lodge {
            origin: url.origin().ascii_serialization(),
            secure,
        })
    }

    pub fn origin(&self) -> &str {
        &self.origin
    }

    pub fn api_url(&self) -> String {
        let rest = self.origin.split_once("://").map_or("", |(_, rest)| rest);
        format!(
            "{}://{rest}/app/read/api",
            if self.secure { "wss" } else { "ws" }
        )
    }
}

fn loopback(host: &str) -> bool {
    let host = host.trim_start_matches('[').trim_end_matches(']');
    host == "localhost"
        || host
            .parse::<std::net::IpAddr>()
            .is_ok_and(|ip| ip.is_loopback())
}

#[cfg(test)]
mod tests {
    use super::{Lodge, Mode, desktop_state_dir_from, load_from};
    use serde::Deserialize;
    use std::collections::HashMap;
    use std::ffi::OsString;
    use std::path::PathBuf;
    use std::sync::atomic::{AtomicUsize, Ordering};

    struct TempGuard(std::path::PathBuf);

    impl TempGuard {
        fn new(label: &str) -> Self {
            static NEXT: AtomicUsize = AtomicUsize::new(0);
            let path = std::env::temp_dir().join(format!(
                "frostread-{label}-{}-{}",
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

    #[test]
    fn parses_lodge_addresses() {
        let good = [
            (
                "https://lodge.example.ts.net",
                "https://lodge.example.ts.net",
                "wss://lodge.example.ts.net/app/read/api",
            ),
            (
                "https://lodge.example.ts.net/",
                "https://lodge.example.ts.net",
                "wss://lodge.example.ts.net/app/read/api",
            ),
            (
                "http://127.0.0.1:8480",
                "http://127.0.0.1:8480",
                "ws://127.0.0.1:8480/app/read/api",
            ),
            (
                "http://localhost:8480",
                "http://localhost:8480",
                "ws://localhost:8480/app/read/api",
            ),
            (
                "http://[::1]:8480",
                "http://[::1]:8480",
                "ws://[::1]:8480/app/read/api",
            ),
        ];
        for (input, origin, api) in good {
            let lodge = Lodge::parse(input).unwrap_or_else(|e| panic!("{input}: {e}"));
            assert_eq!(lodge.origin(), origin, "{input}");
            assert_eq!(lodge.api_url(), api, "{input}");
        }

        for bad in [
            "http://lodge.example.ts.net",
            "http://10.0.0.2",
            "ftp://lodge.example.ts.net",
            "https://lodge.example.ts.net/app",
            "https://u:p@lodge.example.ts.net",
            "https://lodge.example.ts.net/?x=1",
            "https://lodge.example.ts.net/#x",
            "lodge.example.ts.net",
            "",
        ] {
            assert!(Lodge::parse(bad).is_err(), "{bad} parsed");
        }
    }

    #[derive(Deserialize)]
    struct ModeCase {
        name: String,
        #[serde(default)]
        file: Option<String>,
        lodge: String,
        error: bool,
    }

    #[test]
    fn the_shared_mode_table_holds() {
        let json = include_str!("../../../internal/config/testdata/desktop-mode.json");
        let cases: Vec<ModeCase> =
            serde_json::from_str(json).expect("the shared mode table's JSON");
        assert!(
            cases.len() >= 10,
            "the mode table holds {} cases",
            cases.len()
        );
        for case in cases {
            let guard = TempGuard::new(&format!("mode-{}", case.name.replace(' ', "-")));
            let dir = guard.path().to_path_buf();
            std::fs::create_dir_all(dir.join("frostread")).unwrap();
            let path = dir.join("frostread/app.json");
            if let Some(file) = &case.file {
                std::fs::write(&path, file).unwrap();
            }
            let env = |key: &str| {
                if key == "XDG_CONFIG_HOME" {
                    Some(OsString::from(dir.clone()))
                } else if key == "HOME" {
                    Some(OsString::from("/home/user"))
                } else {
                    None
                }
            };
            let got = load_from(env);
            if case.error {
                assert!(got.is_err(), "{}: {got:?}; want an error", case.name);
            } else {
                let mode = got.unwrap_or_else(|e| panic!("{}: {e}", case.name));
                assert_eq!(mode.lodge, case.lodge, "{}", case.name);
            }
        }
    }

    #[derive(Deserialize)]
    struct DirsCase {
        name: String,
        env: HashMap<String, String>,
        state: String,
        mode: String,
        error: bool,
    }

    #[test]
    fn the_shared_dirs_table_holds() {
        let json = include_str!("../../../internal/config/testdata/desktop-dirs.json");
        let cases: Vec<DirsCase> =
            serde_json::from_str(json).expect("the shared dirs table's JSON");
        assert!(
            cases.len() >= 4,
            "the dirs table holds {} cases",
            cases.len()
        );
        for case in cases {
            let env = |key: &str| {
                case.env
                    .get(key)
                    .map(|value| OsString::from(value.as_str()))
            };
            let got_state = desktop_state_dir_from(env);
            let got_mode = super::mode_path_from(env);
            if case.error {
                assert!(
                    got_state.is_err() || got_mode.is_err(),
                    "{}: want an error",
                    case.name
                );
            } else {
                assert_eq!(
                    got_state.unwrap(),
                    PathBuf::from(&case.state),
                    "{}",
                    case.name
                );
                assert_eq!(
                    got_mode.unwrap(),
                    PathBuf::from(&case.mode),
                    "{}",
                    case.name
                );
            }
        }
    }

    #[test]
    fn unknown_and_null_lodge_members_are_local() {
        let guard = TempGuard::new("unknown-mode");
        let dir = guard.path().to_path_buf();
        std::fs::create_dir_all(dir.join("frostread")).unwrap();
        let path = dir.join("frostread/app.json");
        std::fs::write(&path, r#"{"Lodge":"https://example.test","theme":"dark"}"#).unwrap();
        let env = |key: &str| {
            if key == "XDG_CONFIG_HOME" {
                Some(OsString::from(dir.clone()))
            } else {
                None
            }
        };
        assert_eq!(load_from(env).unwrap(), Mode::default());
    }
}
