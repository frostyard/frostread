//! A line bridge between the webview and readd's private Unix socket. It
//! never parses frames: JSON-RPC multiplexing happens in the webview
//! (web/src/rpc/transport/tauri.ts), so the bridge cannot drift from the
//! API (docs/specs/api.md, ADR-0008).

use std::collections::HashMap;
use std::io;

use serde::Serialize;
use tauri::{State, Window, ipc::Channel};
use tokio::io::{
    AsyncBufRead, AsyncBufReadExt, AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt, BufReader,
};
use tokio::net::UnixStream;
use tokio::net::unix::OwnedWriteHalf;
use tokio::sync::Mutex;

use crate::paths;

/// What the webview receives on its channel, in order.
#[derive(Clone, Serialize)]
#[serde(tag = "kind", rename_all = "camelCase")]
pub enum BridgeMessage {
    /// One newline-delimited JSON-RPC message from readd.
    Line { line: String },
    /// The connection ended; no more lines follow.
    Closed { error: String },
}

/// Each window's connection write half, by window label: every window has
/// its own connection. Connecting again replaces the window's connection,
/// which shuts the old one down; closing the window drops it.
#[derive(Default)]
pub struct Bridge {
    writers: Mutex<HashMap<String, OwnedWriteHalf>>,
}

impl Bridge {
    /// Drop a closed window's connection.
    pub async fn forget(&self, label: &str) {
        self.writers.lock().await.remove(label);
    }
}

/// Connect the calling window to readd and stream every received line to
/// `on_message`. A second call for the same window replaces the first
/// connection, so a window keeps exactly one.
#[tauri::command]
pub async fn readd_connect(
    window: Window,
    bridge: State<'_, Bridge>,
    on_message: Channel<BridgeMessage>,
) -> Result<(), String> {
    let path = paths::socket()?;
    let stream = UnixStream::connect(&path)
        .await
        .map_err(|e| format!("connect to readd at {}: {e}", path.display()))?;
    let (read, write) = stream.into_split();
    bridge
        .writers
        .lock()
        .await
        .insert(window.label().to_string(), write);
    tauri::async_runtime::spawn(async move {
        let error = forward(read, |message| on_message.send(message).is_ok()).await;
        let _ = on_message.send(BridgeMessage::Closed { error });
    });
    Ok(())
}

/// Send one JSON-RPC message on the calling window's connection; it must
/// not contain a newline.
#[tauri::command]
pub async fn readd_send(
    window: Window,
    bridge: State<'_, Bridge>,
    line: String,
) -> Result<(), String> {
    let mut guard = bridge.writers.lock().await;
    let writer = guard
        .get_mut(window.label())
        .ok_or("not connected to readd")?;
    send_line(writer, line).await
}

/// MAX_LINE is the longest line readd may send: the kit refuses a line of
/// 16 MiB or more (jsonrpc.MaxMessageSize, docs/specs/api.md), so one byte
/// less is the most that crosses. A longer line ends the connection rather
/// than fill the shell's memory.
pub(crate) const MAX_LINE: usize = (16 << 20) - 1;

/// forward hands every line read from `read` to `on_message`, until the
/// connection ends, and answers why it ended. The channel is the caller's,
/// so a test watches the same loop with no webview.
async fn forward<R: AsyncRead + Unpin>(
    read: R,
    mut on_message: impl FnMut(BridgeMessage) -> bool,
) -> String {
    let mut reader = BufReader::new(read);
    loop {
        match read_line(&mut reader, MAX_LINE).await {
            Ok(Some(line)) => {
                if !on_message(BridgeMessage::Line { line }) {
                    break "the webview stopped listening".to_string();
                }
            }
            Ok(None) => break "readd closed the connection".to_string(),
            Err(e) => break format!("read from readd: {e}"),
        }
    }
}

/// send_line writes one message and its newline to `write`; a message of
/// more than one line is refused.
async fn send_line<W: AsyncWrite + Unpin>(write: &mut W, line: String) -> Result<(), String> {
    let frame = frame(line)?;
    write
        .write_all(&frame)
        .await
        .map_err(|e| format!("write to readd: {e}"))
}

/// read_line reads one line, without its newline, of at most max bytes;
/// None at the end of the input.
async fn read_line<R: AsyncBufRead + Unpin>(r: &mut R, max: usize) -> io::Result<Option<String>> {
    let mut buf = Vec::new();
    if (&mut *r)
        .take(max as u64 + 1)
        .read_until(b'\n', &mut buf)
        .await?
        == 0
    {
        return Ok(None);
    }
    if buf.last() == Some(&b'\n') {
        buf.pop();
    } else if buf.len() > max {
        let over = format!("a line from readd is over {max} bytes");
        return Err(io::Error::new(io::ErrorKind::InvalidData, over));
    }
    String::from_utf8(buf)
        .map(Some)
        .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))
}

/// The bytes on the wire for one message: the line and its newline.
fn frame(line: String) -> Result<Vec<u8>, String> {
    if line.contains('\n') {
        return Err("a frame must be a single line".into());
    }
    let mut frame = line.into_bytes();
    frame.push(b'\n');
    Ok(frame)
}

#[cfg(test)]
mod tests {
    use super::{BridgeMessage, MAX_LINE, forward, read_line, send_line};
    use tokio::io::BufReader;
    use tokio::net::UnixStream;
    use tokio::net::unix::OwnedReadHalf;
    use tokio::sync::mpsc;

    /// block_on runs one test's futures on the shell's runtime.
    fn block_on<F: Future>(f: F) -> F::Output {
        tauri::async_runtime::block_on(f)
    }

    /// a_line_of builds a line of exactly `len` bytes from the characters
    /// JSON escapes: a backslash and a quote.
    fn a_line_of(len: usize) -> String {
        let mut line = "\\\"".repeat(len / 2);
        if len % 2 == 1 {
            line.push('\\');
        }
        assert_eq!(line.len(), len);
        line
    }

    /// pump reads one end of a socket as readd_connect does, and answers the
    /// messages the loop hands over.
    fn pump(read: OwnedReadHalf) -> mpsc::UnboundedReceiver<BridgeMessage> {
        let (tx, rx) = mpsc::unbounded_channel();
        tauri::async_runtime::spawn(forward(read, move |message| tx.send(message).is_ok()));
        rx
    }

    // One line crosses from readd to the webview's channel, and one crosses
    // back to readd, unchanged.
    #[test]
    fn lines_cross_the_bridge_both_ways() {
        block_on(async {
            let (a, b) = UnixStream::pair().unwrap();
            let (ar, mut aw) = a.into_split();
            let (mut br, mut bw) = b.into_split();
            let mut messages = pump(ar);

            let from_readd = r#"{"jsonrpc":"2.0","id":1,"result":{"title":"a \"q\" \\ b"}}"#;
            send_line(&mut bw, from_readd.to_string()).await.unwrap();
            match messages.recv().await.unwrap() {
                BridgeMessage::Line { line } => assert_eq!(line, from_readd),
                BridgeMessage::Closed { error } => panic!("the connection closed: {error}"),
            }

            let from_page = r#"{"jsonrpc":"2.0","id":2,"method":"status","params":{}}"#;
            send_line(&mut aw, from_page.to_string()).await.unwrap();
            let read = read_line(&mut BufReader::new(&mut br), MAX_LINE)
                .await
                .unwrap();
            assert_eq!(read.as_deref(), Some(from_page));

            // readd's end closes, and the loop ends with it.
            drop(bw);
            assert!(messages.recv().await.is_none());
        });
    }

    // The longest line the kit admits, of the characters that grow in JSON,
    // crosses unchanged.
    #[test]
    fn the_longest_line_crosses_unchanged() {
        let mut wire: Vec<u8> = Vec::new();
        block_on(async {
            send_line(&mut wire, a_line_of(MAX_LINE)).await.unwrap();
            let mut lines = 0;
            let reason = forward(&wire[..], |message| {
                if let BridgeMessage::Line { line } = message {
                    assert_eq!(line, a_line_of(MAX_LINE));
                    lines += 1;
                }
                true
            })
            .await;
            assert_eq!(lines, 1);
            assert_eq!(reason, "readd closed the connection");
        });
    }

    // One byte more than the kit admits ends the connection with an error,
    // rather than fill the shell's memory.
    #[test]
    fn a_line_over_the_longest_size_ends_the_connection() {
        let mut input = vec![b'x'; MAX_LINE + 1];
        input.push(b'\n');
        let mut lines = 0;
        let reason = block_on(forward(&input[..], |message| {
            if let BridgeMessage::Line { .. } = message {
                lines += 1;
            }
            true
        }));
        assert_eq!(lines, 0, "a line over {MAX_LINE} bytes must not arrive");
        assert!(reason.contains(&MAX_LINE.to_string()), "{reason}");
    }

    // A message is one line: a newline inside it is refused, not sent.
    #[test]
    fn frames_are_one_line() {
        let mut out: Vec<u8> = Vec::new();
        block_on(async {
            assert_eq!(
                send_line(&mut out, r#"{"jsonrpc":"2.0"}"#.to_string()).await,
                Ok(())
            );
            assert_eq!(out, b"{\"jsonrpc\":\"2.0\"}\n".to_vec());
            assert!(send_line(&mut out, "{}\n{}".to_string()).await.is_err());
        });
    }
}
