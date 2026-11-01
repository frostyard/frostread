//! The Read shell (ADR-0008): one instance with one main window, and a line
//! bridge to readd's private socket. All reading logic lives in readd; the
//! shell moves lines, and opens what the page may not open itself: an
//! article's link, in the system's browser. See
//! docs/plans/0002-the-standalone-desktop-flatpak.md, step 3.2.

mod bridge;
mod paths;

use tauri::webview::NewWindowResponse;
use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_opener::OpenerExt;

fn main() {
    tauri::Builder::default()
        // First, so a second launch hands over before anything else starts.
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            second_launch(app)
        }))
        // The opener opens a link outside the app (ADR-0008, "Links open in
        // the browser"); the dialog is step 3.4's. Neither grants the page a
        // command: capabilities/default.json grants the bridge's two alone.
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(bridge::Bridge::default())
        .invoke_handler(tauri::generate_handler![
            bridge::readd_connect,
            bridge::readd_send
        ])
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::Destroyed = event {
                let app = window.app_handle().clone();
                let label = window.label().to_string();
                tauri::async_runtime::spawn(async move {
                    app.state::<bridge::Bridge>().forget(&label).await;
                });
            }
        })
        .setup(|app| {
            let handle = app.handle().clone();
            // The desktop entry's own page: vite builds desktop.html into
            // build/desktop (web/vite.config.ts).
            WebviewWindowBuilder::new(app, "main", WebviewUrl::App("desktop.html".into()))
                .title("Read")
                .inner_size(1100.0, 760.0)
                .min_inner_size(640.0, 400.0)
                .on_navigation(allowed_navigation)
                // A link clicked in the reader arrives as a new-window
                // request: it opens in the browser, and no window is made.
                .on_new_window(move |url, _| open_new_window(&handle, &url))
                // The window downloads nothing either.
                .on_download(|_, _| false)
                .build()?;
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("frostread failed to start");
}

/// A second launch (single instance) shows the main window.
fn second_launch(app: &tauri::AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
    }
}

/// Only the app itself may load in the main frame: its own bundle, and the
/// blank and srcdoc documents. A link never navigates the window.
fn allowed_navigation(url: &tauri::Url) -> bool {
    match url.scheme() {
        "tauri" => true,
        "about" => matches!(url.path(), "blank" | "srcdoc"),
        _ => false,
    }
}

/// outside_url is the URL the shell opens outside the window: http and
/// https with a host, and mailto with an addressee, without the parts a
/// mail client would act on, such as attaching a file. Every other scheme
/// stays where it is.
fn outside_url(url: &tauri::Url) -> Option<tauri::Url> {
    match url.scheme() {
        "http" | "https" if names_a_host(url) => Some(url.clone()),
        "mailto" if !url.path().is_empty() => Some(mail_url(url.clone())),
        _ => None,
    }
}

/// names_a_host says whether url names a host, as a web URL must.
fn names_a_host(url: &tauri::Url) -> bool {
    url.host_str().is_some_and(|host| !host.is_empty())
}

/// mail_url keeps a mailto link's addressees, subject and body, and drops
/// whatever else its query asks of the mail client, such as attaching a
/// file (attach=), as frostnotes' mail_url does.
fn mail_url(mut url: tauri::Url) -> tauri::Url {
    let kept: Vec<String> = url
        .query()
        .unwrap_or("")
        .split('&')
        .filter(|part| {
            let key = part.split('=').next().unwrap_or("").to_ascii_lowercase();
            matches!(key.as_str(), "to" | "cc" | "bcc" | "subject" | "body")
        })
        .map(str::to_string)
        .collect();
    url.set_query((!kept.is_empty()).then(|| kept.join("&")).as_deref());
    url
}

/// open_new_window is what a link click asks of the shell: the URL opens
/// outside when outside_url names one, and the answer is always Deny, so
/// the webview never holds a second window.
fn open_new_window(app: &tauri::AppHandle, url: &tauri::Url) -> NewWindowResponse<tauri::Wry> {
    if let Some(outside) = outside_url(url) {
        open_outside(app, &outside);
    }
    NewWindowResponse::Deny
}

/// open_outside hands one web or mail link to the system's default handler:
/// in the Flatpak, xdg-open asks the OpenURI portal.
fn open_outside(app: &tauri::AppHandle, url: &tauri::Url) {
    if let Err(e) = app.opener().open_url(url.as_str(), None::<&str>) {
        eprintln!("open {url}: {e}");
    }
}

#[cfg(test)]
mod tests {
    use super::{allowed_navigation, outside_url};

    /// loads says whether the main frame may load url; a URL that does not
    /// parse is no URL of the app's.
    fn loads(url: &str) -> bool {
        tauri::Url::parse(url).is_ok_and(|url| allowed_navigation(&url))
    }

    /// opens says whether the shell hands url to the system.
    fn opens(url: &str) -> bool {
        tauri::Url::parse(url)
            .ok()
            .and_then(|url| outside_url(&url))
            .is_some()
    }

    // The window holds the app's own document and nothing else.
    #[test]
    fn navigates_only_within_the_app() {
        for url in ["tauri://localhost/", "tauri://localhost/index.html"] {
            assert!(loads(url), "{url}");
        }
        for url in ["about:blank", "about:srcdoc"] {
            assert!(loads(url), "{url}");
        }
        for url in [
            "https://example.test/",
            "http://127.0.0.1:5173/",
            "about:config",
        ] {
            assert!(!loads(url), "{url}");
        }
        for url in ["file:///etc/passwd", "mailto:a@e.test"] {
            assert!(!loads(url), "{url}");
        }
    }
    // Only web and mail links leave the app.
    #[test]
    fn opens_only_web_and_mail_links() {
        for url in ["http://example.test", "https://example.test/a?b=1"] {
            assert!(opens(url), "{url}");
        }
        for url in ["mailto:wren@example.test", "mailto:a@e.test?subject=Hi"] {
            assert!(opens(url), "{url}");
        }
        for url in ["mailto:", "http://", "https://", "file:///etc/passwd"] {
            assert!(!opens(url), "{url}");
        }
    }

    // Every other scheme is neither loaded nor opened, so a click on one
    // makes no window and reaches no handler.
    #[test]
    fn denies_every_other_scheme_in_both_places() {
        for url in [
            "javascript:alert(1)",
            "file:///etc/passwd",
            "data:text/html,<script>alert(1)</script>",
            "blob:https://example.test/01234567-89ab-cdef-0123-456789abcdef",
            "ftp://example.test/feed.xml",
            "example://thing",
            "http://",
        ] {
            assert!(!loads(url), "the window loads {url}");
            assert!(!opens(url), "the shell opens {url}");
        }
        // The app's own documents load, and never open outside the app.
        for url in ["tauri://localhost/", "about:blank", "about:srcdoc"] {
            assert!(loads(url), "the window refuses {url}");
            assert!(!opens(url), "the shell opens {url}");
        }
    }

    // A mailto link keeps only what a mail client needs.
    #[test]
    fn mail_links_keep_only_addressees_subject_and_body() {
        let opens_to = |url: &str| {
            outside_url(&tauri::Url::parse(url).unwrap())
                .unwrap()
                .to_string()
        };
        assert_eq!(
            opens_to(
                "mailto:a@e.test?attach=/etc/passwd&subject=Hi%20there&Attachment=x&cc=b@e.test&ATTACH=y&body=a+b"
            ),
            "mailto:a@e.test?subject=Hi%20there&cc=b@e.test&body=a+b"
        );
        assert_eq!(
            opens_to("mailto:a@e.test?attach=/home/u/.ssh/id_ed25519"),
            "mailto:a@e.test"
        );
        assert_eq!(
            opens_to("mailto:a@e.test?%61ttach=x&To=c@e.test"),
            "mailto:a@e.test?To=c@e.test"
        );
        assert_eq!(opens_to("mailto:a@e.test"), "mailto:a@e.test");
    }

    // The capability the window gets: the bridge's two commands, and nothing
    // of Tauri's. A command the page gains arrives here first.
    #[test]
    fn grants_only_the_bridge() {
        let caps = include_str!("../capabilities/default.json");
        let list = caps
            .split("\"permissions\"")
            .nth(1)
            .and_then(|s| s.split(']').next())
            .unwrap();
        let granted: Vec<&str> = list.split('"').skip(1).step_by(2).collect();
        assert_eq!(granted, ["allow-readd-connect", "allow-readd-send"]);
        assert!(
            caps.contains("\"main\""),
            "the capability names no window: {caps}"
        );
    }

    // ADR-0008's CSP, exactly: no network source in the app's document.
    #[test]
    fn holds_the_csp_of_the_decision() {
        const CSP: &str = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; \
                           connect-src ipc: http://ipc.localhost; frame-src 'self'; \
                           form-action 'none'; object-src 'none'";
        let conf = include_str!("../tauri.conf.json");
        assert!(
            conf.contains(&format!("\"csp\": \"{CSP}\"")),
            "the CSP is not ADR-0008's: {conf}"
        );
    }
}
