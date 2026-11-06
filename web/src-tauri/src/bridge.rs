//! A line bridge between the webview and readd: readd's private Unix socket
//! in local mode, or lodge's /app/read/api WebSocket in lodge mode. It never
//! parses frames; JSON-RPC stays in the webview and readd.

use std::collections::HashMap;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::Duration;
use tokio::time::Instant;

use futures_util::{Sink, SinkExt, Stream, StreamExt};
use serde::Serialize;
use tauri::{State, Window, ipc::Channel};
use tokio::io::{
    AsyncBufRead, AsyncBufReadExt, AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt, BufReader,
};
use tokio::net::UnixStream;
use tokio::net::unix::OwnedWriteHalf;
use tokio::sync::{Mutex, RwLock, mpsc, oneshot, watch};
use tokio::task::JoinHandle;
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::protocol::{Message, WebSocketConfig};
use tokio_tungstenite::{MaybeTlsStream, WebSocketStream};

use crate::{paths, remote};

type Socket = WebSocketStream<MaybeTlsStream<tokio::net::TcpStream>>;

fn lodge_websocket_config() -> WebSocketConfig {
    let mut config = WebSocketConfig::default();
    config.max_message_size = Some(LODGE_FRAME_LIMIT);
    config.max_frame_size = Some(LODGE_FRAME_LIMIT);
    config
}

/// One line to lodge, and the result of writing that line.
struct WriteRequest {
    line: String,
    done: oneshot::Sender<Result<(), String>>,
}

/// A window's writer: readd's socket, or lodge's WebSocket.
enum Writer {
    Stream(OwnedWriteHalf),
    Lodge(LodgeConnection),
}

/// Lodge's tasks share one stop sender. Sending true wakes every receiver
/// and stays set; it also survives the replacement or failure of this connection.
struct LodgeConnection {
    writer: mpsc::UnboundedSender<WriteRequest>,
    stop: watch::Sender<bool>,
}

impl LodgeConnection {
    fn stop(&self) {
        self.stop.send_replace(true);
    }
}

impl Drop for LodgeConnection {
    fn drop(&mut self) {
        self.stop();
    }
}

/// A window's connection owns the stop signal for all of its tasks.
struct Connection {
    writer: Writer,
    stop: watch::Sender<bool>,
}

impl Drop for Connection {
    fn drop(&mut self) {
        self.stop.send_replace(true);
    }
}

/// What the webview receives on its channel, in order.
#[derive(Clone, Serialize)]
#[serde(tag = "kind", rename_all = "camelCase")]
pub enum BridgeMessage {
    /// One newline-delimited JSON-RPC message from readd or lodge.
    Line { line: String },
    /// The connection ended; no more lines follow.
    Closed { error: String },
}

/// Each window's connection, by window label.
#[derive(Default)]
pub struct Bridge {
    connections: Mutex<HashMap<String, Connection>>,
    attempts: std::sync::Mutex<HashMap<String, u64>>,
    next_attempt: AtomicU64,
    pub(crate) mode_gate: RwLock<()>,
}

impl Bridge {
    /// Begin a connection attempt before the first await. A newer attempt
    /// invalidates the older attempt for the same window.
    fn begin(&self, label: &str) -> u64 {
        let id = self.next_attempt.fetch_add(1, Ordering::Relaxed) + 1;
        self.attempts.lock().unwrap().insert(label.to_string(), id);
        id
    }

    /// Drop a closed window's connection and invalidate every pending attempt.
    pub async fn forget(&self, label: &str) {
        self.attempts.lock().unwrap().remove(label);
        self.connections.lock().await.remove(label);
    }

    /// Keep a connection only while its attempt is still the window's current attempt.
    async fn keep(
        &self,
        attempt: u64,
        label: &str,
        writer: Writer,
        stop: watch::Sender<bool>,
    ) -> bool {
        let mut connections = self.connections.lock().await;
        let attempts = self.attempts.lock().unwrap();
        if attempts.get(label) != Some(&attempt) {
            stop.send_replace(true);
            drop(writer);
            return false;
        }
        connections.insert(label.to_string(), Connection { writer, stop });
        true
    }
}

/// Lodge closes a session after 90 s without a client data frame.
pub(crate) const PING_EVERY: Duration = Duration::from_secs(30);
// lodge counts data frames, not control frames. This notification is text
// data, and readd's kit ignores it because it has no id, so no answer crosses
// back into the webview's JSON-RPC ids (docs/specs/api.md, "The window in lodge mode").
const PING_TEXT: &str = r#"{"jsonrpc":"2.0","method":"rpc.ping"}"#;

async fn stop_requested(stop: &mut watch::Receiver<bool>) {
    if *stop.borrow() {
        return;
    }
    let _ = stop.wait_for(|stopped| *stopped).await;
}

/// MAX_LINE is the longest line readd may send: the kit refuses a line of
/// 16 MiB or more, so one byte less is the most that crosses.
pub(crate) const MAX_LINE: usize = (16 << 20) - 1;
const LODGE_FRAME_LIMIT: usize = 16 << 20;

/// Connect the calling window to readd, using app.json on every call.
#[tauri::command]
pub async fn readd_connect(
    window: Window,
    bridge: State<'_, Bridge>,
    on_message: Channel<BridgeMessage>,
) -> Result<(), String> {
    let label = window.label().to_string();
    let attempt = bridge.begin(&label);
    readd_connect_inner(
        &bridge.inner().mode_gate,
        |key| std::env::var_os(key),
        |lock, socket| async move {
            stop_local_readd_inner(Instant::now() + Duration::from_secs(30), &lock, &socket).await
        },
        |path| {
            let bridge = bridge.inner();
            let on_message = on_message.clone();
            let label = label.clone();
            async move { connect_local(path, attempt, label, bridge, on_message).await }
        },
        |lodge| {
            let bridge = bridge.inner();
            let on_message = on_message.clone();
            let label = label.clone();
            async move { connect_lodge_mode(lodge, attempt, label, bridge, on_message).await }
        },
    )
    .await
}

/// Choose local or lodge, and make the caller provide only the two connects.
/// This keeps the mode decision testable without a Tauri Window.
pub(crate) async fn readd_connect_inner<FStop, S, FLocal, L, FRemote, R>(
    mode_gate: &RwLock<()>,
    getenv: impl Fn(&str) -> Option<std::ffi::OsString>,
    stop_local: FStop,
    local: FLocal,
    remote: FRemote,
) -> Result<(), String>
where
    FStop: FnOnce(PathBuf, PathBuf) -> S,
    S: Future<Output = Result<(), String>>,
    FLocal: FnOnce(PathBuf) -> L,
    L: Future<Output = Result<(), String>>,
    FRemote: FnOnce(remote::Lodge) -> R,
    R: Future<Output = Result<(), String>>,
{
    let _mode_read = mode_gate.read().await;
    let mode = remote::load_from(&getenv)?;
    if mode.lodge.is_empty() {
        let socket = paths::socket_from(&getenv)?;
        return local(socket).await;
    }

    let lodge = remote::Lodge::parse(&mode.lodge)?;
    let state = remote::desktop_state_dir_from(&getenv)?;
    let socket = paths::socket_from(&getenv)?;
    stop_local(state.join("readd.lock"), socket).await?;
    remote(lodge).await
}

/// Open one local Unix-socket connection and keep exactly one per window.
async fn connect_local(
    path: PathBuf,
    attempt: u64,
    label: String,
    bridge: &Bridge,
    on_message: Channel<BridgeMessage>,
) -> Result<(), String> {
    let stream = UnixStream::connect(&path)
        .await
        .map_err(|e| format!("connect to readd at {}: {e}", path.display()))?;
    let (read, write) = stream.into_split();
    let (stop_tx, mut stop_rx) = watch::channel(false);
    if !bridge
        .keep(attempt, &label, Writer::Stream(write), stop_tx.clone())
        .await
    {
        stop_tx.send_replace(true);
        return Err("the connection was replaced".to_string());
    }
    tauri::async_runtime::spawn(async move {
        let error = forward(read, &mut stop_rx, |message| {
            on_message.send(message).is_ok()
        })
        .await;
        stop_tx.send_replace(true);
        let _ = on_message.send(BridgeMessage::Closed { error });
    });
    Ok(())
}

/// Open lodge's /app/read/api with its ASCII origin.
async fn connect_lodge(lodge: &remote::Lodge) -> Result<Socket, String> {
    let mut req = lodge
        .api_url()
        .into_client_request()
        .map_err(|e| format!("connect to lodge for read: {e}"))?;
    let origin = lodge
        .origin()
        .parse()
        .map_err(|_| format!("lodge's origin {} is not a header", lodge.origin()))?;
    req.headers_mut().insert("Origin", origin);

    let config = lodge_websocket_config();
    let (ws, _) = tokio_tungstenite::connect_async_with_config(req, Some(config), false)
        .await
        .map_err(|e| format!("connect to read through lodge at {}: {e}", lodge.origin()))?;
    Ok(ws)
}

/// Split lodge, keep one writer task, and stream text frames to the webview.
/// This setup has no Tauri types, so a loopback test can start the same tasks.
fn spawn_lodge<F>(ws: Socket, mut on_message: F) -> (LodgeConnection, [JoinHandle<()>; 2])
where
    F: FnMut(BridgeMessage) -> bool + Send + 'static,
{
    let (sink, read) = ws.split();
    let (tx, rx) = mpsc::unbounded_channel();
    let (stop_tx, stop_rx) = watch::channel(false);

    let writer_stop_tx = stop_tx.clone();
    let writer_stop_rx = stop_rx.clone();
    let writer_task = tokio::spawn(lodge_writer(sink, rx, writer_stop_tx, writer_stop_rx));

    let reader_stop_tx = stop_tx.clone();
    let reader_stop_rx = stop_rx;
    let reader_task = tokio::spawn(async move {
        let error = read_frames(read, reader_stop_tx, reader_stop_rx, &mut on_message).await;
        let _ = on_message(BridgeMessage::Closed { error });
    });

    let connection = LodgeConnection {
        writer: tx,
        stop: stop_tx,
    };
    // The app lets both tasks run on their own; a test waits for them.
    (connection, [writer_task, reader_task])
}

async fn connect_lodge_mode(
    lodge: remote::Lodge,
    attempt: u64,
    label: String,
    bridge: &Bridge,
    on_message: Channel<BridgeMessage>,
) -> Result<(), String> {
    let ws = connect_lodge(&lodge).await?;
    let (connection, _tasks) = spawn_lodge(ws, move |message| on_message.send(message).is_ok());
    let stop = connection.stop.clone();
    if !bridge
        .keep(attempt, &label, Writer::Lodge(connection), stop)
        .await
    {
        return Err("the connection was replaced".to_string());
    }
    Ok(())
}

/// Lodge text frames are one BridgeMessage line. This reader is generic so
/// loopback and frame tests do not need a Tauri Window.
async fn read_frames<R, E, F>(
    mut read: R,
    stop_tx: watch::Sender<bool>,
    mut stop_rx: watch::Receiver<bool>,
    mut on_message: F,
) -> String
where
    R: Stream<Item = Result<Message, E>> + Unpin,
    E: std::fmt::Display,
    F: FnMut(BridgeMessage) -> bool,
{
    let reason = loop {
        tokio::select! {
            next = read.next() => match next {
                Some(Ok(Message::Text(text))) => {
                    let text = text.as_str();
                    let line = text.strip_suffix('\n').unwrap_or(text).to_string();
                    if line.len() > MAX_LINE {
                        break format!("lodge sent a line over {MAX_LINE} bytes");
                    }
                    if line.contains('\n') {
                        break "lodge sent a frame of several lines".to_string();
                    }
                    if !on_message(BridgeMessage::Line { line }) {
                        break "the webview stopped listening".to_string();
                    }
                }
                Some(Ok(Message::Binary(_))) => {
                    break "lodge sent a binary frame".to_string();
                }
                Some(Ok(Message::Close(frame))) => {
                    break match frame {
                        Some(frame) if !frame.reason.is_empty() => {
                            format!("lodge closed the connection: {}", frame.reason)
                        }
                        _ => "lodge closed the connection".to_string(),
                    };
                }
                Some(Ok(_)) => {}
                Some(Err(e)) => break format!("read from lodge: {e}"),
                None => break "lodge closed the connection".to_string(),
            },
            _ = stop_requested(&mut stop_rx) => {
                break "the connection was replaced".to_string();
            }
        }
    };
    stop_tx.send_replace(true);
    reason
}

/// One writer task owns lodge's sink. Its interval starts after 30 s,
/// and write replies go through each request's oneshot.
async fn lodge_writer<S, E>(
    mut sink: S,
    mut requests: mpsc::UnboundedReceiver<WriteRequest>,
    stop_tx: watch::Sender<bool>,
    mut stop_rx: watch::Receiver<bool>,
) where
    S: Sink<Message, Error = E> + Unpin,
    E: std::fmt::Display,
{
    let start = tokio::time::Instant::now() + PING_EVERY;
    let mut ping = tokio::time::interval_at(start, PING_EVERY);
    ping.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);

    loop {
        tokio::select! {
            request = requests.recv() => match request {
                Some(request) => {
                    let send = sink.send(Message::text(request.line));
                    tokio::pin!(send);
                    let result = tokio::select! {
                        result = &mut send => result.map_err(|e| format!("write to lodge: {e}")),
                        _ = stop_requested(&mut stop_rx) => {
                            let _ = request.done.send(Err("write to lodge: the writer stopped".to_string()));
                            break;
                        }
                    };
                    let failed = result.is_err();
                    let _ = request.done.send(result);
                    if failed {
                        break;
                    }
                }
                None => break,
            },
            _ = ping.tick() => {
                let send = sink.send(Message::text(PING_TEXT));
                tokio::pin!(send);
                let ping_result = tokio::select! {
                    result = &mut send => result,
                    _ = stop_requested(&mut stop_rx) => break,
                };
                if ping_result.is_err() {
                    break;
                }
            },
            _ = stop_requested(&mut stop_rx) => break,
        }
    }
    stop_tx.send_replace(true);
}

/// Send one JSON-RPC message; local framing keeps the newline, lodge sends text.
#[tauri::command]
pub async fn readd_send(
    window: Window,
    bridge: State<'_, Bridge>,
    line: String,
) -> Result<(), String> {
    if line.contains('\n') {
        return Err("a frame must be a single line".into());
    }

    let mut guard = bridge.connections.lock().await;
    let connection = guard
        .get_mut(window.label())
        .ok_or("not connected to readd")?;
    match &mut connection.writer {
        Writer::Stream(write) => send_line(write, line).await,
        Writer::Lodge(connection) => {
            let (done_tx, done_rx) = oneshot::channel();
            connection
                .writer
                .send(WriteRequest {
                    line,
                    done: done_tx,
                })
                .map_err(|_| "not connected to lodge".to_string())?;
            drop(guard);
            done_rx
                .await
                .map_err(|_| "write to lodge: the writer stopped".to_string())?
        }
    }
}

/// Stop a desktop readd before lodge mode opens a WebSocket.
pub(crate) async fn stop_local_readd_inner(
    deadline: Instant,
    lock_path: &Path,
    socket_path: &Path,
) -> Result<(), String> {
    loop {
        let file = match std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(lock_path)
        {
            Ok(file) => file,
            Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(()),
            Err(e) => return Err(format!("open {}: {e}", lock_path.display())),
        };

        if let Ok(()) = file.try_lock() {
            let _ = file.unlock();
            return Ok(());
        }

        if Instant::now() >= deadline {
            return Err("This computer's reader did not stop. Frostread does not connect to lodge while it runs.".to_string());
        }

        if let Ok(stream) = UnixStream::connect(socket_path).await {
            let (read, mut write) = stream.into_split();
            let _ = write
                .write_all(
                    br#"{"jsonrpc":"2.0","id":1,"method":"rpc.hello","params":{"protocol":1,"client":"read-desktop"}}
{"jsonrpc":"2.0","id":2,"method":"daemon.shutdown","params":{}}
"#,
                )
                .await;
            let mut reader = BufReader::new(read);
            let _ = tokio::time::timeout(Duration::from_secs(2), async {
                for _ in 0..2 {
                    match read_line(&mut reader, MAX_LINE).await {
                        Ok(Some(_)) => {}
                        Ok(None) | Err(_) => break,
                    }
                }
            })
            .await;
        }

        let now = Instant::now();
        let remaining = deadline.saturating_duration_since(now);
        tokio::time::sleep(remaining.min(Duration::from_secs(1))).await;
    }
}

/// forward hands every line read from `read` to `on_message`, until the
/// connection ends, and answers why it ended.
async fn forward<R: AsyncRead + Unpin, F: FnMut(BridgeMessage) -> bool>(
    read: R,
    stop_rx: &mut watch::Receiver<bool>,
    mut on_message: F,
) -> String {
    let mut reader = BufReader::new(read);
    loop {
        tokio::select! {
            line = read_line(&mut reader, MAX_LINE) => match line {
                Ok(Some(line)) => {
                    if !on_message(BridgeMessage::Line { line }) {
                        break "the webview stopped listening".to_string();
                    }
                }
                Ok(None) => break "readd closed the connection".to_string(),
                Err(e) => break format!("read from readd: {e}"),
            },
            _ = stop_requested(stop_rx) => {
                break "the connection was replaced".to_string();
            }
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
    use super::*;
    use std::pin::Pin;
    use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
    use std::sync::{Arc, Mutex as StdMutex};
    use std::task::{Context, Poll};

    use tokio::net::{TcpListener, UnixListener};
    use tokio_tungstenite::tungstenite::handshake::server::{
        Callback, ErrorResponse, Request, Response,
    };
    use tokio_tungstenite::tungstenite::protocol::CloseFrame;

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

    fn block_on<F: Future>(f: F) -> F::Output {
        tauri::async_runtime::block_on(f)
    }

    fn a_line_of(len: usize) -> String {
        let mut line = "\\\"".repeat(len / 2);
        if len % 2 == 1 {
            line.push('\\');
        }
        assert_eq!(line.len(), len);
        line
    }

    fn pump(read: tokio::net::unix::OwnedReadHalf) -> mpsc::UnboundedReceiver<BridgeMessage> {
        let (tx, rx) = mpsc::unbounded_channel();
        tauri::async_runtime::spawn(async move {
            let (_stop_tx, mut stop_rx) = watch::channel(false);
            forward(read, &mut stop_rx, move |message| tx.send(message).is_ok()).await;
        });
        rx
    }

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

            drop(bw);
            assert!(messages.recv().await.is_none());
        });
    }

    #[test]
    fn the_longest_line_crosses_unchanged() {
        block_on(async {
            send_line(&mut Vec::new(), a_line_of(MAX_LINE))
                .await
                .unwrap();
            let mut lines = 0;
            let (_stop_tx, mut stop_rx) = watch::channel(false);
            let reason = forward(
                &a_line_of(MAX_LINE).into_bytes()[..],
                &mut stop_rx,
                |message| {
                    if let BridgeMessage::Line { line } = message {
                        assert_eq!(line, a_line_of(MAX_LINE));
                        lines += 1;
                    }
                    true
                },
            )
            .await;
            assert_eq!(lines, 1);
            assert_eq!(reason, "readd closed the connection");
        });
    }

    #[test]
    fn a_line_over_the_longest_size_ends_the_connection() {
        let mut input = vec![b'x'; MAX_LINE + 1];
        input.push(b'\n');
        let mut lines = 0;
        let (_stop_tx, mut stop_rx) = watch::channel(false);
        let reason = block_on(forward(&input[..], &mut stop_rx, |message| {
            if let BridgeMessage::Line { .. } = message {
                lines += 1;
            }
            true
        }));
        assert_eq!(lines, 0);
        assert!(reason.contains(&MAX_LINE.to_string()), "{reason}");
    }

    #[test]
    fn frames_are_one_line() {
        block_on(async {
            let mut out: Vec<u8> = Vec::new();
            assert_eq!(
                send_line(&mut out, r#"{"jsonrpc":"2.0"}"#.to_string()).await,
                Ok(())
            );
            assert_eq!(out, b"{\"jsonrpc\":\"2.0\"}\n".to_vec());
            assert!(send_line(&mut out, "{}\n{}".to_string()).await.is_err());
        });
    }

    #[derive(Clone, Default)]
    struct TestSink {
        messages: Arc<StdMutex<Vec<Message>>>,
    }

    impl Sink<Message> for TestSink {
        type Error = io::Error;

        fn poll_ready(
            self: Pin<&mut Self>,
            _cx: &mut Context<'_>,
        ) -> Poll<Result<(), Self::Error>> {
            Poll::Ready(Ok(()))
        }

        fn start_send(self: Pin<&mut Self>, message: Message) -> Result<(), Self::Error> {
            self.messages.lock().unwrap().push(message);
            Ok(())
        }

        fn poll_flush(
            self: Pin<&mut Self>,
            _cx: &mut Context<'_>,
        ) -> Poll<Result<(), Self::Error>> {
            Poll::Ready(Ok(()))
        }

        fn poll_close(
            self: Pin<&mut Self>,
            _cx: &mut Context<'_>,
        ) -> Poll<Result<(), Self::Error>> {
            Poll::Ready(Ok(()))
        }
    }

    struct PendingSink;

    impl Sink<Message> for PendingSink {
        type Error = io::Error;

        fn poll_ready(
            self: Pin<&mut Self>,
            _cx: &mut Context<'_>,
        ) -> Poll<Result<(), Self::Error>> {
            Poll::Pending
        }

        fn start_send(self: Pin<&mut Self>, _message: Message) -> Result<(), Self::Error> {
            Ok(())
        }

        fn poll_flush(
            self: Pin<&mut Self>,
            _cx: &mut Context<'_>,
        ) -> Poll<Result<(), Self::Error>> {
            Poll::Pending
        }

        fn poll_close(
            self: Pin<&mut Self>,
            _cx: &mut Context<'_>,
        ) -> Poll<Result<(), Self::Error>> {
            Poll::Pending
        }
    }

    struct FailingSink;

    impl Sink<Message> for FailingSink {
        type Error = io::Error;

        fn poll_ready(
            self: Pin<&mut Self>,
            _cx: &mut Context<'_>,
        ) -> Poll<Result<(), Self::Error>> {
            Poll::Ready(Ok(()))
        }

        fn start_send(self: Pin<&mut Self>, _message: Message) -> Result<(), Self::Error> {
            Err(io::Error::new(
                io::ErrorKind::BrokenPipe,
                "test sink failed",
            ))
        }

        fn poll_flush(
            self: Pin<&mut Self>,
            _cx: &mut Context<'_>,
        ) -> Poll<Result<(), Self::Error>> {
            Poll::Ready(Ok(()))
        }

        fn poll_close(
            self: Pin<&mut Self>,
            _cx: &mut Context<'_>,
        ) -> Poll<Result<(), Self::Error>> {
            Poll::Ready(Ok(()))
        }
    }

    #[tokio::test(flavor = "current_thread")]
    async fn forgotten_attempt_is_not_kept() {
        let bridge = Bridge::default();
        let id = bridge.begin("main");
        bridge.forget("main").await;

        let (tx, _rx) = mpsc::unbounded_channel();
        let (stop_tx, stop_rx) = watch::channel(false);
        let connection = LodgeConnection {
            writer: tx,
            stop: stop_tx.clone(),
        };
        assert!(
            !bridge
                .keep(id, "main", Writer::Lodge(connection), stop_tx)
                .await
        );
        assert!(
            *stop_rx.borrow(),
            "stale connection kept its stop signal clear"
        );
        assert!(bridge.connections.lock().await.get("main").is_none());
    }

    #[tokio::test(flavor = "current_thread")]
    async fn newer_attempt_stays_when_older_attempt_finishes() {
        let bridge = Bridge::default();
        let id1 = bridge.begin("main");
        let id2 = bridge.begin("main");

        let (tx2, _rx2) = mpsc::unbounded_channel();
        let (stop2_tx, stop2_rx) = watch::channel(false);
        let connection2 = LodgeConnection {
            writer: tx2,
            stop: stop2_tx.clone(),
        };
        assert!(
            bridge
                .keep(id2, "main", Writer::Lodge(connection2), stop2_tx)
                .await
        );

        let (tx1, _rx1) = mpsc::unbounded_channel();
        let (stop1_tx, stop1_rx) = watch::channel(false);
        let connection1 = LodgeConnection {
            writer: tx1,
            stop: stop1_tx.clone(),
        };
        assert!(
            !bridge
                .keep(id1, "main", Writer::Lodge(connection1), stop1_tx)
                .await
        );
        assert!(*stop1_rx.borrow(), "old attempt did not stop");
        assert!(
            !*stop2_rx.borrow(),
            "new attempt was stopped by the old one"
        );
        assert!(bridge.connections.lock().await.contains_key("main"));

        bridge.forget("main").await;
        assert!(*stop2_rx.borrow(), "forget did not stop the kept attempt");
    }

    #[tokio::test(start_paused = true, flavor = "current_thread")]
    async fn pending_lodge_send_stops_on_replacement() {
        let (stop_tx, stop_rx) = watch::channel(false);
        let (tx, rx) = mpsc::unbounded_channel();
        let task = tokio::spawn(lodge_writer(PendingSink, rx, stop_tx.clone(), stop_rx));

        let (done_tx, done_rx) = oneshot::channel();
        tx.send(WriteRequest {
            line: "waiting".to_string(),
            done: done_tx,
        })
        .unwrap();
        tokio::task::yield_now().await;

        stop_tx.send_replace(true);
        let done = tokio::time::timeout(Duration::from_secs(1), done_rx)
            .await
            .expect("a pending lodge send ignored the stop signal")
            .unwrap();
        assert_eq!(done, Err("write to lodge: the writer stopped".to_string()));

        tokio::time::timeout(Duration::from_secs(1), task)
            .await
            .expect("the lodge writer stayed alive after stop")
            .unwrap();
    }

    #[test]
    fn lodge_websocket_config_has_memory_bounds() {
        let config = lodge_websocket_config();
        assert_eq!(config.max_message_size, Some(LODGE_FRAME_LIMIT));
        assert_eq!(config.max_frame_size, Some(LODGE_FRAME_LIMIT));
    }

    #[tokio::test(start_paused = true, flavor = "current_thread")]
    async fn lodge_writer_pings_and_lines() {
        let sink = TestSink::default();
        let (tx, rx) = mpsc::unbounded_channel();
        let (stop_tx, stop_rx) = watch::channel(false);
        let task = tokio::spawn(lodge_writer(sink.clone(), rx, stop_tx, stop_rx));

        let (done_tx, done_rx) = oneshot::channel();
        tx.send(WriteRequest {
            line: "hello".to_string(),
            done: done_tx,
        })
        .unwrap();
        assert_eq!(done_rx.await.unwrap(), Ok(()));

        tokio::time::advance(Duration::from_secs(29)).await;
        tokio::task::yield_now().await;
        assert_eq!(sink.messages.lock().unwrap().len(), 1);

        tokio::time::advance(Duration::from_secs(1)).await;
        tokio::task::yield_now().await;
        let messages = sink.messages.lock().unwrap().clone();
        assert_eq!(messages.len(), 2);
        assert_eq!(messages[1].to_text().unwrap(), PING_TEXT);

        tokio::time::advance(Duration::from_secs(30)).await;
        tokio::task::yield_now().await;
        let messages = sink.messages.lock().unwrap().clone();
        assert_eq!(messages.len(), 3);
        assert_eq!(messages[2].to_text().unwrap(), PING_TEXT);

        drop(tx);
        task.await.unwrap();
    }

    #[tokio::test(start_paused = true, flavor = "current_thread")]
    async fn lodge_writer_failure_stops_reader() {
        let (stop_tx, stop_rx) = watch::channel(false);
        let (_tx, rx) = mpsc::unbounded_channel::<WriteRequest>();
        let writer = tokio::spawn(lodge_writer(
            FailingSink,
            rx,
            stop_tx.clone(),
            stop_rx.clone(),
        ));
        let reader = tokio::spawn(read_frames(
            futures_util::stream::pending::<Result<Message, io::Error>>(),
            stop_tx,
            stop_rx,
            |_| true,
        ));

        tokio::time::advance(Duration::from_secs(30)).await;
        tokio::task::yield_now().await;
        assert_eq!(writer.await.unwrap(), ());
        assert_eq!(reader.await.unwrap(), "the connection was replaced");
    }

    #[tokio::test(flavor = "current_thread")]
    async fn dropping_lodge_connection_stops_reader_writer_and_socket() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server_closed = Arc::new(StdMutex::new(false));
        let server_closed_inner = server_closed.clone();

        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let mut ws = tokio_tungstenite::accept_async(stream).await.unwrap();
            let closed = match tokio::time::timeout(Duration::from_secs(2), ws.next()).await {
                Ok(Some(Ok(Message::Close(_)))) | Ok(None) | Ok(Some(Err(_))) => true,
                Ok(Some(Ok(_))) | Err(_) => false,
            };
            *server_closed_inner.lock().unwrap() = closed;
        });

        let lodge = remote::Lodge::parse(&format!("http://{addr}")).unwrap();
        let ws = connect_lodge(&lodge).await.unwrap();
        let (messages_tx, mut messages_rx) = mpsc::unbounded_channel();
        let (connection, tasks) = spawn_lodge(ws, move |message| messages_tx.send(message).is_ok());
        let [writer_task, reader_task] = tasks;
        drop(connection);

        let closed = tokio::time::timeout(Duration::from_secs(2), messages_rx.recv())
            .await
            .expect("the reader kept the replaced connection open")
            .unwrap();
        match closed {
            BridgeMessage::Closed { error } => {
                assert_eq!(error, "the connection was replaced");
            }
            BridgeMessage::Line { .. } => panic!("the replaced connection sent a line"),
        }

        tokio::time::timeout(Duration::from_secs(2), writer_task)
            .await
            .expect("the writer task stayed open")
            .unwrap();
        tokio::time::timeout(Duration::from_secs(2), reader_task)
            .await
            .expect("the reader task stayed open")
            .unwrap();
        tokio::time::timeout(Duration::from_secs(2), server)
            .await
            .expect("the server did not observe the socket close")
            .unwrap();
        assert!(
            *server_closed.lock().unwrap(),
            "the server did not see the socket close"
        );
    }

    struct OriginCallback {
        seen: Arc<StdMutex<Option<(String, String)>>>,
    }

    impl Callback for OriginCallback {
        fn on_request(
            self,
            request: &Request,
            response: Response,
        ) -> Result<Response, ErrorResponse> {
            *self.seen.lock().unwrap() = Some((
                request.uri().path().to_string(),
                request
                    .headers()
                    .get("origin")
                    .and_then(|value| value.to_str().ok())
                    .unwrap_or_default()
                    .to_string(),
            ));
            Ok(response)
        }
    }

    #[tokio::test(flavor = "current_thread")]
    async fn loopback_lodge_text_origin_and_line() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let origin_seen = Arc::new(StdMutex::new(None));
        let origin_seen_server = origin_seen.clone();

        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let mut ws = tokio_tungstenite::accept_hdr_async(
                stream,
                OriginCallback {
                    seen: origin_seen_server,
                },
            )
            .await
            .unwrap();
            ws.send(Message::text("from lodge\n")).await.unwrap();
            let line = ws.next().await.unwrap().unwrap();
            assert_eq!(line.to_text().unwrap(), "to lodge");
            let _ = ws.close(None).await;
        });

        let lodge = remote::Lodge::parse(&format!("http://{addr}")).unwrap();
        let ws = connect_lodge(&lodge).await.unwrap();
        let (mut sink, read) = ws.split();
        let (stop_tx, stop_rx) = watch::channel(false);
        let (tx, mut rx) = mpsc::unbounded_channel();
        let reader = tokio::spawn(read_frames(read, stop_tx, stop_rx, move |message| {
            tx.send(message).is_ok()
        }));

        sink.send(Message::text("to lodge")).await.unwrap();
        let received = rx.recv().await.unwrap();
        match received {
            BridgeMessage::Line { line } => assert_eq!(line, "from lodge"),
            BridgeMessage::Closed { error } => panic!("closed: {error}"),
        }
        server.await.unwrap();
        drop(sink);
        let _ = reader.await.unwrap();

        let (path, origin) = origin_seen.lock().unwrap().clone().unwrap();
        assert_eq!(path, "/app/read/api");
        assert_eq!(origin, lodge.origin());
    }

    #[tokio::test(flavor = "current_thread")]
    async fn loopback_lodge_bad_frames_end() {
        for bad in ["binary", "newline", "close"] {
            let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
            let addr = listener.local_addr().unwrap();
            let server = tokio::spawn(async move {
                let (stream, _) = listener.accept().await.unwrap();
                let mut ws = tokio_tungstenite::accept_async(stream).await.unwrap();
                match bad {
                    "binary" => ws.send(Message::Binary(vec![1, 2].into())).await.unwrap(),
                    "newline" => ws.send(Message::text("two\nlines")).await.unwrap(),
                    "close" => ws.send(Message::Close(Some(CloseFrame { code: tokio_tungstenite::tungstenite::protocol::frame::coding::CloseCode::Normal, reason: "done".into() }))).await.unwrap(),
                    _ => unreachable!(),
                }
            });
            let lodge = remote::Lodge::parse(&format!("http://{addr}")).unwrap();
            let ws = connect_lodge(&lodge).await.unwrap();
            let (_sink, read) = ws.split();
            let (stop_tx, stop_rx) = watch::channel(false);
            let reason = read_frames(read, stop_tx, stop_rx, |_| true).await;
            match bad {
                "binary" => assert_eq!(reason, "lodge sent a binary frame"),
                "newline" => assert_eq!(reason, "lodge sent a frame of several lines"),
                "close" => assert!(reason.contains("lodge closed the connection"), "{reason}"),
                _ => unreachable!(),
            }
            server.await.unwrap();
        }
    }

    #[tokio::test(flavor = "current_thread")]
    async fn lodge_text_at_the_line_limit_arrives_once() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let mut ws = tokio_tungstenite::accept_async(stream).await.unwrap();
            ws.send(Message::text("x".repeat(MAX_LINE))).await.unwrap();
            let _ = ws.close(None).await;
        });

        let lodge = remote::Lodge::parse(&format!("http://{addr}")).unwrap();
        let ws = connect_lodge(&lodge).await.unwrap();
        let (messages_tx, mut messages_rx) = mpsc::unbounded_channel();
        let (connection, tasks) = spawn_lodge(ws, move |message| messages_tx.send(message).is_ok());

        match messages_rx.recv().await.unwrap() {
            BridgeMessage::Line { line } => assert_eq!(line.len(), MAX_LINE),
            BridgeMessage::Closed { error } => panic!("exact-limit frame closed: {error}"),
        }
        drop(connection);
        for task in tasks {
            tokio::time::timeout(Duration::from_secs(2), task)
                .await
                .expect("the exact-limit task stayed alive")
                .unwrap();
        }
        server.await.unwrap();
    }

    #[tokio::test(flavor = "current_thread")]
    async fn lodge_text_over_the_line_limit_ends_with_closed() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let mut ws = tokio_tungstenite::accept_async(stream).await.unwrap();
            let _ = ws.send(Message::text("x".repeat(MAX_LINE + 1))).await;
        });

        let lodge = remote::Lodge::parse(&format!("http://{addr}")).unwrap();
        let ws = connect_lodge(&lodge).await.unwrap();
        let (messages_tx, mut messages_rx) = mpsc::unbounded_channel();
        let (connection, tasks) = spawn_lodge(ws, move |message| messages_tx.send(message).is_ok());

        let closed = messages_rx.recv().await.unwrap();
        match closed {
            BridgeMessage::Closed { error } => {
                assert!(error.contains(&MAX_LINE.to_string()), "{error}");
            }
            BridgeMessage::Line { .. } => panic!("over-limit frame arrived as a line"),
        }
        drop(connection);
        for task in tasks {
            tokio::time::timeout(Duration::from_secs(2), task)
                .await
                .expect("the over-limit task stayed alive")
                .unwrap();
        }
        let _ = server.await;
    }

    #[tokio::test(flavor = "current_thread")]
    async fn lodge_text_over_the_lodge_frame_limit_ends_with_closed() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let mut ws = tokio_tungstenite::accept_async(stream).await.unwrap();
            let _ = ws.send(Message::text("x".repeat(32 << 20))).await;
        });

        let lodge = remote::Lodge::parse(&format!("http://{addr}")).unwrap();
        let ws = connect_lodge(&lodge).await.unwrap();
        let (messages_tx, mut messages_rx) = mpsc::unbounded_channel();
        let (connection, tasks) = spawn_lodge(ws, move |message| messages_tx.send(message).is_ok());

        let closed = messages_rx.recv().await.unwrap();
        match closed {
            BridgeMessage::Closed { error } => {
                assert!(
                    error.starts_with("read from lodge:"),
                    "the limit did not come from tungstenite: {error}"
                );
                assert!(!error.contains("lodge sent a line over"), "{error}");
            }
            BridgeMessage::Line { .. } => panic!("frame over lodge's limit arrived as a line"),
        }
        drop(connection);
        for task in tasks {
            tokio::time::timeout(Duration::from_secs(2), task)
                .await
                .expect("the frame-limit task stayed alive")
                .unwrap();
        }
        let _ = server.await;
    }

    #[tokio::test(start_paused = true, flavor = "current_thread")]
    async fn stop_with_no_lock_file_is_immediate() {
        let guard = TempGuard::new("no-lock");
        let dir = guard.path();
        let lock = dir.join("readd.lock");
        let socket = dir.join("readd.sock");
        stop_local_readd_inner(Instant::now() + Duration::from_secs(30), &lock, &socket)
            .await
            .unwrap();
    }

    #[tokio::test(start_paused = true, flavor = "current_thread")]
    async fn stop_held_lock_asks_fake_readd_and_succeeds() {
        let guard = TempGuard::new("stop-held");
        let dir = guard.path();
        let lock = dir.join("readd.lock");
        let socket = dir.join("readd.sock");
        let holder = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .open(&lock)
            .unwrap();
        holder.try_lock().unwrap();

        let listener = UnixListener::bind(&socket).unwrap();
        let unlock = holder.try_clone().unwrap();
        let fake = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let (read, mut write) = stream.into_split();
            let mut reader = BufReader::new(read);
            let hello = read_line(&mut reader, MAX_LINE).await.unwrap().unwrap();
            let shutdown = read_line(&mut reader, MAX_LINE).await.unwrap().unwrap();
            assert!(hello.contains("rpc.hello"));
            assert!(shutdown.contains("daemon.shutdown"));
            write.write_all(b"{}\n{}\n").await.unwrap();
            unlock.unlock().unwrap();
        });

        stop_local_readd_inner(Instant::now() + Duration::from_secs(30), &lock, &socket)
            .await
            .unwrap();
        fake.await.unwrap();
        drop(holder);
    }

    #[tokio::test(start_paused = true, flavor = "current_thread")]
    async fn stop_held_forever_returns_at_deadline() {
        let guard = TempGuard::new("stop-ever");
        let dir = guard.path();
        let lock = dir.join("readd.lock");
        let socket = dir.join("readd.sock");
        let holder = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .open(&lock)
            .unwrap();
        holder.try_lock().unwrap();

        let error =
            stop_local_readd_inner(Instant::now() + Duration::from_secs(30), &lock, &socket)
                .await
                .unwrap_err();
        assert_eq!(
            error,
            "This computer's reader did not stop. Frostread does not connect to lodge while it runs."
        );
        drop(holder);
    }

    #[tokio::test(flavor = "current_thread")]
    async fn lodge_mode_never_falls_back_to_local() {
        let guard = TempGuard::new("never-fallback");
        let dir = guard.path().to_path_buf();
        std::fs::create_dir_all(dir.join("config/frostread")).unwrap();
        std::fs::create_dir_all(dir.join("data")).unwrap();
        std::fs::write(
            dir.join("config/frostread/app.json"),
            r#"{"lodge":"http://127.0.0.1:1"}"#,
        )
        .unwrap();

        let local_socket = dir.join("readd.sock");
        let listener = std::os::unix::net::UnixListener::bind(&local_socket).unwrap();
        listener.set_nonblocking(true).unwrap();
        let local_called = Arc::new(AtomicBool::new(false));
        let local_called_inner = local_called.clone();

        let port_listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let closed_addr = port_listener.local_addr().unwrap();
        drop(port_listener);
        std::fs::write(
            dir.join("config/frostread/app.json"),
            format!(r#"{{"lodge":"http://{closed_addr}"}}"#),
        )
        .unwrap();

        let env = |key: &str| -> Option<std::ffi::OsString> {
            match key {
                "XDG_CONFIG_HOME" => Some(std::ffi::OsString::from(dir.join("config"))),
                "XDG_DATA_HOME" => Some(std::ffi::OsString::from(dir.join("data"))),
                "READ_SOCKET" => Some(std::ffi::OsString::from(local_socket.clone())),
                _ => None,
            }
        };

        let result = readd_connect_inner(
            &RwLock::new(()),
            env,
            |_lock, _socket| async { Ok(()) },
            move |_path| {
                local_called_inner.store(true, Ordering::SeqCst);
                async { Ok(()) }
            },
            |lodge| async move { connect_lodge(&lodge).await.map(|_| ()) },
        )
        .await;

        assert!(result.is_err());
        assert!(!local_called.load(Ordering::SeqCst));
        assert!(matches!(listener.accept(), Err(e) if e.kind() == io::ErrorKind::WouldBlock));
    }

    #[tokio::test(flavor = "current_thread")]
    async fn lodge_mode_stops_before_remote_when_stop_fails() {
        let guard = TempGuard::new("stop-before-remote");
        let dir = guard.path().to_path_buf();
        std::fs::create_dir_all(dir.join("config/frostread")).unwrap();
        std::fs::create_dir_all(dir.join("data")).unwrap();
        std::fs::write(
            dir.join("config/frostread/app.json"),
            r#"{"lodge":"https://lodge.example.ts.net"}"#,
        )
        .unwrap();

        let local_socket = dir.join("readd.sock");
        let env = |key: &str| -> Option<std::ffi::OsString> {
            match key {
                "XDG_CONFIG_HOME" => Some(std::ffi::OsString::from(dir.join("config"))),
                "XDG_DATA_HOME" => Some(std::ffi::OsString::from(dir.join("data"))),
                "READ_SOCKET" => Some(std::ffi::OsString::from(local_socket.clone())),
                _ => None,
            }
        };

        let local_called = Arc::new(AtomicBool::new(false));
        let remote_called = Arc::new(AtomicBool::new(false));
        let local_called_inner = local_called.clone();
        let remote_called_inner = remote_called.clone();

        let result = readd_connect_inner(
            &RwLock::new(()),
            env,
            |_lock, _socket| async {
                Err("This computer's reader did not stop. Frostread does not connect to lodge while it runs.".to_string())
            },
            move |_path| {
                local_called_inner.store(true, Ordering::SeqCst);
                async { Ok(()) }
            },
            move |_lodge| {
                remote_called_inner.store(true, Ordering::SeqCst);
                async { Ok(()) }
            },
        )
        .await;

        assert_eq!(
            result.unwrap_err(),
            "This computer's reader did not stop. Frostread does not connect to lodge while it runs."
        );
        assert!(!local_called.load(Ordering::SeqCst));
        assert!(!remote_called.load(Ordering::SeqCst));
    }
}
