//! Save text files where the user chooses.
//! (docs/plans/0002-the-standalone-desktop-flatpak.md, step 3.4)

use std::path::{Path, PathBuf};

use tauri_plugin_dialog::{DialogExt, FilePath};

use crate::{paths, portal};

/// Picker asks the platform for one place to save a file.
pub(crate) trait Picker {
    fn pick(
        &self,
        name: &str,
        filter_name: &str,
        extension: &str,
    ) -> impl std::future::Future<Output = Result<Option<PathBuf>, String>> + Send;
}

/// PortalPicker asks the FileChooser portal in a Flatpak.
pub(crate) struct PortalPicker;

impl Picker for PortalPicker {
    async fn pick(
        &self,
        name: &str,
        filter_name: &str,
        extension: &str,
    ) -> Result<Option<PathBuf>, String> {
        portal::save_file("Save file", name, filter_name, extension).await
    }
}

/// DialogPicker asks Tauri's save dialog outside a Flatpak, over the window
/// that asked.
pub(crate) struct DialogPicker {
    window: tauri::WebviewWindow,
}

impl DialogPicker {
    pub(crate) fn new(window: tauri::WebviewWindow) -> Self {
        Self { window }
    }
}

impl Picker for DialogPicker {
    async fn pick(
        &self,
        name: &str,
        filter_name: &str,
        extension: &str,
    ) -> Result<Option<PathBuf>, String> {
        let (answer, wait) = tokio::sync::oneshot::channel::<Option<FilePath>>();
        self.window
            .dialog()
            .file()
            .set_parent(&self.window)
            .set_file_name(name)
            .add_filter(filter_name, &[extension])
            .save_file(move |path| {
                let _ = answer.send(path);
            });
        match wait
            .await
            .map_err(|_| "the save dialog ended without an answer".to_string())?
        {
            None => Ok(None),
            Some(FilePath::Path(path)) => Ok(Some(path)),
            Some(_) => Err("the save dialog did not name a local path".to_string()),
        }
    }
}

/// save_text checks the plain name, asks a picker, and writes the exact text.
/// It answers false when the user cancels, and true after a write.
pub(crate) async fn save_text(
    picker: &impl Picker,
    name: &str,
    text: &str,
) -> Result<bool, String> {
    if name.is_empty() {
        return Err("the file name is empty".to_string());
    }
    if name == "." || name == ".." {
        return Err("the file name is not a plain file name".to_string());
    }
    if name.contains('/') || name.contains('\\') || name.contains('\0') {
        return Err("the file name is not a plain file name".to_string());
    }
    let extension = Path::new(name)
        .extension()
        .and_then(|value| value.to_str())
        .filter(|value| !value.is_empty())
        .ok_or_else(|| "the file name has no extension".to_string())?;
    let filter_name = extension.to_ascii_uppercase();
    let path = picker.pick(name, &filter_name, extension).await?;
    let Some(path) = path else {
        return Ok(false);
    };
    tokio::fs::write(&path, text)
        .await
        // The error names its kind only: the path the user picked stays out
        // of the message, as it stays out of the log.
        .map_err(|e| format!("cannot write the selected file: {}", e.kind()))?;
    Ok(true)
}

/// save_file asks the shell to save text at a place the user picks.
#[tauri::command]
pub(crate) async fn save_file(
    window: tauri::WebviewWindow,
    name: String,
    text: String,
) -> Result<bool, String> {
    if paths::in_flatpak() {
        save_text(&PortalPicker, &name, &text).await
    } else {
        save_text(&DialogPicker::new(window), &name, &text).await
    }
}

#[cfg(test)]
mod tests {
    use std::sync::{Arc, Mutex};

    use super::{Picker, save_text};

    struct StandIn {
        path: Option<std::path::PathBuf>,
        asked: Arc<Mutex<Vec<(String, String, String)>>>,
    }

    impl Picker for StandIn {
        async fn pick(
            &self,
            name: &str,
            filter_name: &str,
            extension: &str,
        ) -> Result<Option<std::path::PathBuf>, String> {
            self.asked.lock().unwrap().push((
                name.to_string(),
                filter_name.to_string(),
                extension.to_string(),
            ));
            Ok(self.path.clone())
        }
    }

    #[tokio::test]
    async fn it_writes_the_exact_text_to_the_picked_path() {
        let dir = std::env::temp_dir().join(format!("frostread-files-{}", std::process::id()));
        let _ = tokio::fs::create_dir_all(&dir).await;
        let path = dir.join("chosen.opml");
        let _ = tokio::fs::remove_file(&path).await;
        let asked = Arc::new(Mutex::new(Vec::new()));
        let picker = StandIn {
            path: Some(path.clone()),
            asked: Arc::clone(&asked),
        };

        assert_eq!(
            save_text(&picker, "frostread.opml", "<opml>exact</opml>").await,
            Ok(true)
        );
        assert_eq!(
            tokio::fs::read_to_string(&path).await.unwrap(),
            "<opml>exact</opml>"
        );
        assert_eq!(
            *asked.lock().unwrap(),
            vec![(
                "frostread.opml".to_string(),
                "OPML".to_string(),
                "opml".to_string()
            )]
        );

        let _ = tokio::fs::remove_file(&path).await;
        let _ = tokio::fs::remove_dir_all(&dir).await;
    }

    #[tokio::test]
    async fn a_cancel_writes_nothing_and_answers_false() {
        let dir = std::env::temp_dir().join(format!("frostread-cancel-{}", std::process::id()));
        let path = dir.join("not-written.opml");
        let _ = tokio::fs::remove_dir_all(&dir).await;
        let asked = Arc::new(Mutex::new(Vec::new()));
        let picker = StandIn {
            path: None,
            asked: Arc::clone(&asked),
        };

        assert_eq!(
            save_text(&picker, "frostread.opml", "nothing").await,
            Ok(false)
        );
        assert!(!path.exists());
        assert_eq!(asked.lock().unwrap().len(), 1);
    }

    #[tokio::test]
    async fn bad_names_are_refused_before_the_picker_is_asked() {
        let asked = Arc::new(Mutex::new(Vec::new()));
        let picker = StandIn {
            path: Some(std::env::temp_dir().join("unused.opml")),
            asked: Arc::clone(&asked),
        };
        for name in [
            "",
            "..",
            ".",
            "bad/name.opml",
            "bad\\name.opml",
            "bad\0name.opml",
            "no-extension",
        ] {
            assert!(save_text(&picker, name, "text").await.is_err(), "{name:?}");
        }
        assert!(
            asked.lock().unwrap().is_empty(),
            "a bad name must not ask the picker"
        );
    }

    #[tokio::test]
    async fn the_opml_filter_and_extension_are_passed_to_the_picker() {
        let asked = Arc::new(Mutex::new(Vec::new()));
        let picker = StandIn {
            path: None,
            asked: Arc::clone(&asked),
        };
        assert_eq!(
            save_text(&picker, "frostread.opml", "text").await,
            Ok(false)
        );
        assert_eq!(
            *asked.lock().unwrap(),
            vec![(
                "frostread.opml".to_string(),
                "OPML".to_string(),
                "opml".to_string()
            )]
        );
    }
}
