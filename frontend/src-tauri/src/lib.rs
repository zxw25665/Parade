use std::path::PathBuf;
use std::process::{Child, ChildStdin, ChildStdout, ChildStderr, Command, Stdio};
use std::sync::{Arc, Mutex};
use std::sync::atomic::{AtomicU64, Ordering};

use std::collections::HashMap;
use std::io::{BufRead, BufReader, Write};

use once_cell::sync::Lazy;
use tauri::RunEvent;
use tauri::Emitter;
use tracing::{error, info};
use tracing_appender::rolling::{RollingFileAppender, Rotation};
use tracing_subscriber::{fmt, prelude::*, EnvFilter};

static DAEMON_BRIDGE: Lazy<Arc<Mutex<Option<DaemonBridge>>>> =
    Lazy::new(|| Arc::new(Mutex::new(None)));

static APP_HANDLE: Lazy<Arc<Mutex<Option<tauri::AppHandle>>>> =
    Lazy::new(|| Arc::new(Mutex::new(None)));

static LAST_DAEMON_ERROR: Lazy<Mutex<Option<String>>> =
    Lazy::new(|| Mutex::new(None));

#[cfg(windows)]
#[allow(dead_code)]
const CREATE_NO_WINDOW: u32 = 0x08000000;

type PendingCall = std::sync::mpsc::Sender<Result<serde_json::Value, String>>;

pub struct DaemonBridge {
    stdin: Arc<Mutex<ChildStdin>>,
    pending: Arc<Mutex<HashMap<u64, PendingCall>>>,
    next_id: AtomicU64,
    child: Mutex<Option<Child>>,
}

impl DaemonBridge {
    pub fn new(
        child: Child,
        stdin: ChildStdin,
        stdout: ChildStdout,
        stderr: ChildStderr,
        app_handle: tauri::AppHandle,
    ) -> Result<Self, anyhow::Error> {
        let stdin = Arc::new(Mutex::new(stdin));
        let pending: Arc<Mutex<HashMap<u64, PendingCall>>> = Arc::new(Mutex::new(HashMap::new()));

        // Spawn stdout reader thread
        let pending_clone = pending.clone();
        let app_handle_clone = app_handle.clone();
        std::thread::spawn(move || {
            let reader = BufReader::new(stdout);
            for line in reader.lines() {
                match line {
                    Ok(line) => {
                        let line = line.trim().to_string();
                        if line.is_empty() {
                            continue;
                        }
                        match serde_json::from_str::<serde_json::Value>(&line) {
                            Ok(value) => {
                                // Check if it's an event (method == "event")
                                if value.get("method").and_then(|m| m.as_str()) == Some("event") {
                                    if let Some(params) = value.get("params") {
                                        let event_name = params.get("event")
                                            .and_then(|e| e.as_str())
                                            .unwrap_or("");
                                        let data = params.get("data")
                                            .cloned()
                                            .unwrap_or(serde_json::Value::Null);
                                        let _ = app_handle_clone.emit(event_name, data);
                                    }
                                } else if let Some(id) = value.get("id") {
                                    // It's a response — route to pending call
                                    if let Some(id_num) = id.as_u64() {
                                        let mut pending_lock = pending_clone.lock().unwrap();
                                        if let Some(sender) = pending_lock.remove(&id_num) {
                                            if let Some(err) = value.get("error") {
                                                let msg = err.get("message")
                                                    .and_then(|m| m.as_str())
                                                    .unwrap_or("Unknown error");
                                                let _ = sender.send(Err(msg.to_string()));
                                            } else {
                                                let result = value.get("result")
                                                    .cloned()
                                                    .unwrap_or(serde_json::Value::Null);
                                                let _ = sender.send(Ok(result));
                                            }
                                        }
                                    }
                                }
                            }
                            Err(e) => {
                                tracing::warn!("[daemon] Failed to parse stdout line: {} — line: {}", e, line);
                            }
                        }
                    }
                    Err(e) => {
                        tracing::error!("[daemon] Stdout reader error: {}", e);
                        break;
                    }
                }
            }
            tracing::info!("[daemon] Stdout reader loop exited");
        });

        // Spawn stderr forwarder thread
        std::thread::spawn(move || {
            let reader = BufReader::new(stderr);
            for line in reader.lines() {
                match line {
                    Ok(line) => {
                        let line = line.trim().to_string();
                        if !line.is_empty() {
                            tracing::warn!("[daemon] {}", line);
                        }
                    }
                    Err(_) => break,
                }
            }
        });

        Ok(Self {
            stdin,
            pending,
            next_id: AtomicU64::new(1),
            child: Mutex::new(Some(child)),
        })
    }

    pub fn call(&self, method: &str, params: Option<serde_json::Value>) -> Result<serde_json::Value, anyhow::Error> {
        let id = self.next_id.fetch_add(1, Ordering::SeqCst);

        let request = serde_json::json!({
            "jsonrpc": "2.0",
            "id": id,
            "method": method,
            "params": params,
        });

        let (tx, rx) = std::sync::mpsc::channel();

        {
            let mut pending_lock = self.pending.lock().unwrap();
            pending_lock.insert(id, tx);
        }

        {
            let mut stdin_lock = self.stdin.lock().unwrap();
            let req_str = serde_json::to_string(&request)?;
            stdin_lock.write_all(req_str.as_bytes())?;
            stdin_lock.write_all(b"\n")?;
            stdin_lock.flush()?;
        }

        // Wait for response (30s timeout)
        match rx.recv_timeout(std::time::Duration::from_secs(30)) {
            Ok(result) => result.map_err(|e| anyhow::anyhow!("{}", e)),
            Err(std::sync::mpsc::RecvTimeoutError::Timeout) => {
                // Clean up pending entry
                let mut pending_lock = self.pending.lock().unwrap();
                pending_lock.remove(&id);
                Err(anyhow::anyhow!("RPC call '{}' timed out after 30s", method))
            }
            Err(std::sync::mpsc::RecvTimeoutError::Disconnected) => {
                Err(anyhow::anyhow!("Daemon connection lost"))
            }
        }
    }

    pub fn shutdown(&self) {
        if let Ok(mut child_opt) = self.child.lock() {
            if let Some(mut child) = child_opt.take() {
                let _ = child.kill();
                let _ = child.wait();
            }
        }
    }
}

fn get_data_dir() -> PathBuf {
    // 1. Explicit env var override
    if let Ok(dir) = std::env::var("PARADE_DATA_DIR") {
        let p = PathBuf::from(&dir);
        if p.exists() {
            return p;
        }
    }

    // 2. Default: XDG data directory (~/.local/share/parade on Linux)
    dirs::data_local_dir()
        .unwrap_or_else(|| PathBuf::from("."))
        .join("parade")
}

fn get_daemon_path() -> PathBuf {
    let exe_dir = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|p| p.to_path_buf()))
        .unwrap_or_default();

    // Search for Tauri sidecar pattern: parade-daemon-{target_triple}{exe_suffix}
    // Tauri's externalBin bundles the daemon with the target triple in the name,
    // and the release bundle places it alongside the main exe.
    if let Ok(entries) = std::fs::read_dir(&exe_dir) {
        for entry in entries.flatten() {
            let name = entry.file_name();
            let name_str = name.to_string_lossy();
            if name_str.starts_with("parade-daemon-") || name_str.starts_with("parade-daemon.") {
                let path = entry.path();
                if path.is_file() {
                    #[cfg(unix)]
                    {
                        use std::os::unix::fs::PermissionsExt;
                        if let Ok(meta) = path.metadata() {
                            if meta.permissions().mode() & 0o111 != 0 {
                                return path;
                            }
                        }
                        continue;
                    }
                    #[cfg(not(unix))]
                    {
                        return path;
                    }
                }
            }
        }
    }

    // Fallback to explicit candidates
    let candidates = [
        exe_dir.join("parade"),
        exe_dir.join("parade.exe"),
        exe_dir.join("parade-daemon"),
        exe_dir.join("parade-daemon.exe"),
        PathBuf::from("../../../parade"),
        PathBuf::from("../parade"),
    ];

    for candidate in &candidates {
        if candidate.exists() {
            return candidate.clone();
        }
    }

    PathBuf::from("parade")
}

fn setup_logging() {
    let log_dir = get_data_dir().join("logs");
    let _ = std::fs::create_dir_all(&log_dir);

    let file_appender = RollingFileAppender::new(
        Rotation::DAILY,
        &log_dir,
        "parade-tauri.log",
    );

    let (non_blocking, guard) = tracing_appender::non_blocking(file_appender);
    std::mem::forget(guard);

    tracing_subscriber::registry()
        .with(EnvFilter::new("info"))
        .with(fmt::layer().with_writer(non_blocking))
        .with(fmt::layer().with_writer(std::io::stderr))
        .init();
}

fn spawn_daemon() -> Result<DaemonBridge, anyhow::Error> {
    let daemon_path = get_daemon_path();
    let data_dir = get_data_dir();
    let data_dir_str = data_dir.to_string_lossy().to_string();

    info!("Spawning daemon: {:?} with data-dir: {:?}", daemon_path, data_dir);

    let args = vec![
        "daemon".to_string(),
        "--data-dir".to_string(),
        data_dir_str,
    ];

    let mut cmd = Command::new(&daemon_path);
    cmd.args(&args);
    cmd.stdin(Stdio::piped());
    cmd.stdout(Stdio::piped());
    cmd.stderr(Stdio::piped());

    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        cmd.creation_flags(CREATE_NO_WINDOW);
    }

    let mut child = cmd.spawn()?;

    let stdin = child.stdin.take()
        .ok_or_else(|| anyhow::anyhow!("Failed to get daemon stdin"))?;
    let stdout = child.stdout.take()
        .ok_or_else(|| anyhow::anyhow!("Failed to get daemon stdout"))?;
    let stderr = child.stderr.take()
        .ok_or_else(|| anyhow::anyhow!("Failed to get daemon stderr"))?;

    let app_handle = APP_HANDLE.lock().unwrap()
        .clone()
        .ok_or_else(|| anyhow::anyhow!("APP_HANDLE not set"))?;

    let bridge = DaemonBridge::new(child, stdin, stdout, stderr, app_handle)?;

    info!("Daemon spawned successfully");
    Ok(bridge)
}

fn shutdown_daemon() {
    info!("Shutting down daemon...");

    if let Ok(mut bridge_lock) = DAEMON_BRIDGE.lock() {
        if let Some(bridge) = bridge_lock.take() {
            bridge.shutdown();
        }
    }
}

#[tauri::command]
fn on_foreground() -> Result<(), String> {
    let bridge_lock = DAEMON_BRIDGE.lock().map_err(|e| e.to_string())?;
    let bridge = bridge_lock.as_ref().ok_or("Daemon not connected")?;
    bridge
        .call("OnForeground", None)
        .map_err(|e| e.to_string())?;
    Ok(())
}

#[tauri::command]
fn write_log_file(file_path: String, content: String) -> Result<(), String> {
    std::fs::write(&file_path, &content).map_err(|e| e.to_string())
}

#[tauri::command]
fn get_daemon_error() -> Result<Option<String>, String> {
    Ok(LAST_DAEMON_ERROR.lock().map_err(|e| e.to_string())?.clone())
}

#[tauri::command]
fn call_daemon(method: String, params: Option<serde_json::Value>) -> Result<serde_json::Value, String> {
    let bridge_lock = DAEMON_BRIDGE.lock().map_err(|e| e.to_string())?;
    let bridge = bridge_lock.as_ref().ok_or("Daemon not connected")?;
    bridge.call(&method, params).map_err(|e| e.to_string())
}

#[tauri::command]
fn register(password: String) -> Result<(), String> {
    call_daemon("Register".to_string(), Some(serde_json::json!([password])))?;
    Ok(())
}

#[tauri::command]
fn login(password: String) -> Result<(), String> {
    call_daemon("Login".to_string(), Some(serde_json::json!([password])))?;
    Ok(())
}

#[tauri::command]
fn logout() -> Result<(), String> {
    call_daemon("Logout".to_string(), None)?;
    Ok(())
}

#[tauri::command]
fn check_has_identity() -> Result<bool, String> {
    let result: bool = serde_json::from_value(call_daemon("CheckHasIdentity".to_string(), None)?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn join_team(secret: String) -> Result<String, String> {
    let result: String = serde_json::from_value(call_daemon("JoinTeam".to_string(), Some(serde_json::json!([secret])))?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn join_team_with_name(name: String, secret: String) -> Result<String, String> {
    let result: String = serde_json::from_value(call_daemon("JoinTeamWithName".to_string(), Some(serde_json::json!([name, secret])))?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn leave_team(team_id: String) -> Result<(), String> {
    call_daemon("LeaveTeam".to_string(), Some(serde_json::json!([team_id])))?;
    Ok(())
}

#[tauri::command]
fn switch_team(team_id: String) -> Result<(), String> {
    call_daemon("SwitchTeam".to_string(), Some(serde_json::json!([team_id])))?;
    Ok(())
}

#[tauri::command]
fn list_teams() -> Result<Vec<serde_json::Value>, String> {
    let result: Vec<serde_json::Value> = serde_json::from_value(call_daemon("ListTeams".to_string(), None)?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn get_active_team() -> Result<String, String> {
    let result: String = serde_json::from_value(call_daemon("GetActiveTeam".to_string(), None)?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn send_team_chat(text: String) -> Result<(), String> {
    call_daemon("SendTeamChat".to_string(), Some(serde_json::json!([text])))?;
    Ok(())
}

#[tauri::command]
fn send_private_chat(target_uuid: String, text: String) -> Result<(), String> {
    call_daemon("SendPrivateChat".to_string(), Some(serde_json::json!([target_uuid, text])))?;
    Ok(())
}

#[tauri::command]
fn get_conversation_messages(conv_id: String, limit: i32, offset: i32) -> Result<Vec<serde_json::Value>, String> {
    let result: Vec<serde_json::Value> = serde_json::from_value(
        call_daemon("GetConversationMessages".to_string(), Some(serde_json::json!([conv_id, limit, offset])))?
    ).map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn list_conversations() -> Result<Vec<serde_json::Value>, String> {
    let result: Vec<serde_json::Value> = serde_json::from_value(call_daemon("ListConversations".to_string(), None)?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn start_private_conversation(peer_uuid: String) -> Result<String, String> {
    let result: String = serde_json::from_value(call_daemon("StartPrivateConversation".to_string(), Some(serde_json::json!([peer_uuid])))?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn get_peers() -> Result<Vec<serde_json::Value>, String> {
    let result: Vec<serde_json::Value> = serde_json::from_value(call_daemon("GetPeers".to_string(), None)?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn get_peers_with_status() -> Result<Vec<serde_json::Value>, String> {
    let result: Vec<serde_json::Value> = serde_json::from_value(call_daemon("GetPeersWithStatus".to_string(), None)?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn connect_to_peer(ip_address: String) -> Result<serde_json::Value, String> {
    let result: serde_json::Value = serde_json::from_value(call_daemon("ConnectToPeer".to_string(), Some(serde_json::json!([ip_address])))?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn list_saved_peers() -> Result<Vec<String>, String> {
    let result: Vec<String> = serde_json::from_value(call_daemon("ListSavedPeers".to_string(), None)?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn save_peer(ip_address: String) -> Result<(), String> {
    call_daemon("SavePeer".to_string(), Some(serde_json::json!([ip_address])))?;
    Ok(())
}

#[tauri::command]
fn remove_peer(ip_address: String) -> Result<(), String> {
    call_daemon("RemovePeer".to_string(), Some(serde_json::json!([ip_address])))?;
    Ok(())
}

#[tauri::command]
fn share_directory(path: String) -> Result<(), String> {
    call_daemon("ShareDirectory".to_string(), Some(serde_json::json!([path])))?;
    Ok(())
}

#[tauri::command]
fn unshare_directory(path: String) -> Result<(), String> {
    call_daemon("UnshareDirectory".to_string(), Some(serde_json::json!([path])))?;
    Ok(())
}

#[tauri::command]
fn get_directory_children(path: String) -> Result<Vec<serde_json::Value>, String> {
    let result: Vec<serde_json::Value> = serde_json::from_value(call_daemon("GetDirectoryChildren".to_string(), Some(serde_json::json!([path])))?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn get_remote_directory_children(target_uuid: String, path: String) -> Result<Vec<serde_json::Value>, String> {
    let result: Vec<serde_json::Value> = serde_json::from_value(call_daemon("GetRemoteDirectoryChildren".to_string(), Some(serde_json::json!([target_uuid, path])))?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn start_download(target_uuid: String, virtual_path: String, local_save_path: String) -> Result<(), String> {
    call_daemon("StartDownload".to_string(), Some(serde_json::json!([target_uuid, virtual_path, local_save_path])))?;
    Ok(())
}

#[tauri::command]
fn get_default_download_dir() -> Result<String, String> {
    let result: String = serde_json::from_value(call_daemon("GetDefaultDownloadDir".to_string(), None)?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn create_share_group(name: String) -> Result<String, String> {
    let result: String = serde_json::from_value(call_daemon("CreateShareGroup".to_string(), Some(serde_json::json!([name])))?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn list_share_groups() -> Result<Vec<serde_json::Value>, String> {
    let result: Vec<serde_json::Value> = serde_json::from_value(call_daemon("ListShareGroups".to_string(), None)?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn add_directory_to_share_group(group_id: String, dir_path: String) -> Result<(), String> {
    call_daemon("AddDirectoryToShareGroup".to_string(), Some(serde_json::json!([group_id, dir_path])))?;
    Ok(())
}

#[tauri::command]
fn remove_directory_from_share_group(group_id: String, dir_path: String) -> Result<(), String> {
    call_daemon("RemoveDirectoryFromShareGroup".to_string(), Some(serde_json::json!([group_id, dir_path])))?;
    Ok(())
}

#[tauri::command]
fn delete_share_group(group_id: String) -> Result<(), String> {
    call_daemon("DeleteShareGroup".to_string(), Some(serde_json::json!([group_id])))?;
    Ok(())
}

#[tauri::command]
fn get_share_group_dirs(group_id: String) -> Result<Vec<serde_json::Value>, String> {
    let result: Vec<serde_json::Value> = serde_json::from_value(call_daemon("GetShareGroupDirs".to_string(), Some(serde_json::json!([group_id])))?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn get_pub_key() -> Result<String, String> {
    let result: String = serde_json::from_value(call_daemon("GetPubKey".to_string(), None)?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[tauri::command]
fn export_logs() -> Result<serde_json::Value, String> {
    let result: serde_json::Value = serde_json::from_value(call_daemon("ExportLogs".to_string(), None)?)
        .map_err(|e| e.to_string())?;
    Ok(result)
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    setup_logging();
    info!("Parade Tauri app starting...");

    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            info!("Setting up Tauri app...");

            // Set APP_HANDLE before spawning daemon
            *APP_HANDLE.lock().unwrap() = Some(app.handle().clone());

            match spawn_daemon() {
                Ok(bridge) => {
                    *DAEMON_BRIDGE.lock().unwrap() = Some(bridge);
                    let _ = app.handle().emit("daemon_ready", ());
                }
                Err(e) => {
                    let err_msg = e.to_string();
                    error!("Failed to spawn daemon: {}", err_msg);
                    *LAST_DAEMON_ERROR.lock().unwrap() = Some(err_msg.clone());
                    let _ = app.handle().emit("daemon_error", err_msg);
                }
            }

            Ok(())
        })
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                window.hide().unwrap_or_default();
                api.prevent_close();
            }
        })
        .invoke_handler(tauri::generate_handler![
            call_daemon,
            get_daemon_error,
            register,
            login,
            logout,
            check_has_identity,
            join_team,
            join_team_with_name,
            leave_team,
            switch_team,
            list_teams,
            get_active_team,
            send_team_chat,
            send_private_chat,
            get_conversation_messages,
            list_conversations,
            start_private_conversation,
            get_peers,
            get_peers_with_status,
            connect_to_peer,
            list_saved_peers,
            save_peer,
            remove_peer,
            share_directory,
            unshare_directory,
            get_directory_children,
            get_remote_directory_children,
            start_download,
            get_default_download_dir,
            create_share_group,
            list_share_groups,
            add_directory_to_share_group,
            remove_directory_from_share_group,
            delete_share_group,
            get_share_group_dirs,
            get_pub_key,
            export_logs,
            on_foreground,
            write_log_file,
        ])
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app_handle, event| {
            if let RunEvent::ExitRequested { api, code, .. } = event {
                if code == Some(0) {
                    shutdown_daemon();
                    app_handle.exit(0);
                }
                api.prevent_exit();
            }
        });
}
