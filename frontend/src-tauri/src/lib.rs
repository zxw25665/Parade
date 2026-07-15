use std::path::PathBuf;
use std::process::{Command, Stdio};
use std::sync::{Arc, Mutex};

use once_cell::sync::Lazy;
use serde::{Deserialize, Serialize};
use tauri::{RunEvent};
use tracing::{error, info};
use tracing_appender::rolling::{RollingFileAppender, Rotation};
use tracing_subscriber::{fmt, prelude::*, EnvFilter};

static DAEMON_HANDLE: Lazy<Arc<Mutex<Option<std::process::Child>>>> =
    Lazy::new(|| Arc::new(Mutex::new(None)));

static UDS_CLIENT: Lazy<Arc<Mutex<Option<UDSConnection>>>> =
    Lazy::new(|| Arc::new(Mutex::new(None)));

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct JsonRpcRequest {
    pub jsonrpc: String,
    pub id: Option<serde_json::Value>,
    pub method: String,
    #[serde(default)]
    pub params: Option<serde_json::Value>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct JsonRpcResponse {
    pub jsonrpc: String,
    #[serde(default)]
    pub id: serde_json::Value,
    #[serde(default)]
    pub result: Option<serde_json::Value>,
    #[serde(default)]
    pub error: Option<RpcError>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RpcError {
    pub code: i32,
    pub message: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EventPayload {
    pub event: String,
    pub data: Option<serde_json::Value>,
}

#[cfg(unix)]
mod uds {
    use super::*;
    use std::io::{Read, Write};
    use std::os::unix::net::UnixStream;

    pub struct UDSConnection {
        socket_path: PathBuf,
    }

    impl UDSConnection {
        pub fn new(socket_path: PathBuf) -> Result<Self, anyhow::Error> {
            Ok(Self { socket_path })
        }

        pub fn connect_stream(&self) -> Result<UnixStream, anyhow::Error> {
            let stream = UnixStream::connect(&self.socket_path)?;
            stream.set_read_timeout(Some(std::time::Duration::from_secs(30)))?;
            stream.set_write_timeout(Some(std::time::Duration::from_secs(30)))?;
            Ok(stream)
        }

        pub fn call(&self, method: &str, params: Option<serde_json::Value>) -> Result<serde_json::Value, anyhow::Error> {
            let mut stream = self.connect_stream()?;

            let req = JsonRpcRequest {
                jsonrpc: "2.0".to_string(),
                id: Some(serde_json::json!(1)),
                method: method.to_string(),
                params,
            };

            let req_json = serde_json::to_string(&req)?;
            stream.write_all(format!("{}\n", req_json).as_bytes())?;
            stream.flush()?;

            let mut buf = vec![0u8; 65536];
            let n = stream.read(&mut buf)?;
            buf.truncate(n);

            let resp: JsonRpcResponse = serde_json::from_slice(&buf)?;
            if let Some(err) = resp.error {
                return Err(anyhow::anyhow!("RPC error {}: {}", err.code, err.message));
            }
            Ok(resp.result.unwrap_or(serde_json::Value::Null))
        }
    }
}

#[cfg(windows)]
mod uds {
    use super::*;
    use std::io::{Read, Write};

    pub struct UDSConnection {
        pipe_name: String,
    }

    impl UDSConnection {
        pub fn new(pipe_name: String) -> Result<Self, anyhow::Error> {
            Ok(Self { pipe_name })
        }

        pub fn call(&self, method: &str, params: Option<serde_json::Value>) -> Result<serde_json::Value, anyhow::Error> {
            let pipe_path = format!("\\\\.\\pipe\\{}", self.pipe_name);

            let mut pipe = std::fs::OpenOptions::new()
                .read(true)
                .write(true)
                .open(&pipe_path)
                .map_err(|e| anyhow::anyhow!("Failed to open pipe {}: {}", pipe_path, e))?;

            let req = JsonRpcRequest {
                jsonrpc: "2.0".to_string(),
                id: Some(serde_json::json!(1)),
                method: method.to_string(),
                params,
            };

            let req_json = serde_json::to_string(&req)?;
            pipe.write_all(format!("{}\n", req_json).as_bytes())?;
            pipe.flush()?;

            let mut buf = vec![0u8; 65536];
            let n = pipe.read(&mut buf)?;
            buf.truncate(n);

            let resp: JsonRpcResponse = serde_json::from_slice(&buf)?;
            if let Some(err) = resp.error {
                return Err(anyhow::anyhow!("RPC error {}: {}", err.code, err.message));
            }
            Ok(resp.result.unwrap_or(serde_json::Value::Null))
        }
    }
}

#[cfg(unix)]
use uds::UDSConnection;

#[cfg(windows)]
use uds::UDSConnection;

fn get_data_dir() -> PathBuf {
    if let Ok(dir) = std::env::var("PARADE_DATA_DIR") {
        let p = PathBuf::from(&dir);
        if p.exists() {
            return p;
        }
    }

    let exe_dir = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|p| p.to_path_buf()))
        .unwrap_or_default();

    let daemon_candidates = [
        exe_dir.join("parade"),
        exe_dir.join("parade.exe"),
        PathBuf::from("/home/wzx/Parade/parade"),
        PathBuf::from("../../../parade"),
        PathBuf::from("../parade"),
    ];

    for candidate in &daemon_candidates {
        if let Some(parent) = candidate.parent() {
            if parent.join(".parade_identity").exists() {
                return parent.to_path_buf();
            }
        }
    }

    dirs::data_local_dir()
        .unwrap_or_else(|| PathBuf::from("."))
        .join("parade")
}

fn get_daemon_path() -> PathBuf {
    let exe_dir = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|p| p.to_path_buf()))
        .unwrap_or_default();

    let candidates = [
        exe_dir.join("parade"),
        exe_dir.join("parade.exe"),
        PathBuf::from("/home/wzx/Parade/parade"),
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

fn get_uds_path() -> PathBuf {
    #[cfg(unix)]
    {
        PathBuf::from("/tmp/parade.sock")
    }
    #[cfg(windows)]
    {
        PathBuf::from("parade")
    }
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

fn spawn_daemon() -> Result<(), anyhow::Error> {
    let daemon_path = get_daemon_path();
    let data_dir = get_data_dir();
    let uds_path = get_uds_path();

    info!("Spawning daemon: {:?} with data-dir: {:?}, uds: {:?}", daemon_path, data_dir, uds_path);

    let args = vec![
        "daemon".to_string(),
        "--data-dir".to_string(),
        data_dir.to_string_lossy().to_string(),
        "--uds".to_string(),
        uds_path.to_string_lossy().to_string(),
    ];

    #[cfg(unix)]
    {
        let child = Command::new(&daemon_path)
            .args(&args)
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()?;

        let mut handle = DAEMON_HANDLE.lock().unwrap();
        *handle = Some(child);
    }

    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x08000000;

        let child = Command::new(&daemon_path)
            .args(&args)
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .creation_flags(CREATE_NO_WINDOW)
            .spawn()?;

        let mut handle = DAEMON_HANDLE.lock().unwrap();
        *handle = Some(child);
    }

    std::thread::sleep(std::time::Duration::from_millis(500));

    let client = UDSConnection::new(uds_path)?;
    let mut conn = UDS_CLIENT.lock().unwrap();
    *conn = Some(client);

    info!("Daemon spawned successfully");
    Ok(())
}

fn shutdown_daemon() {
    info!("Shutting down daemon...");

    {
        let mut conn = UDS_CLIENT.lock().unwrap();
        *conn = None;
    }

    if let Ok(mut handle) = DAEMON_HANDLE.lock() {
        if let Some(mut child) = handle.take() {
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

#[tauri::command]
fn call_daemon(method: String, params: Option<serde_json::Value>) -> Result<serde_json::Value, String> {
    let conn = UDS_CLIENT.lock().map_err(|e| e.to_string())?;
    let client = conn.as_ref().ok_or("Daemon not connected")?;

    client.call(&method, params).map_err(|e| e.to_string())
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
        .setup(|_app| {
            info!("Setting up Tauri app...");

            if let Err(e) = spawn_daemon() {
                error!("Failed to spawn daemon: {}", e);
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
            register,
            login,
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
