/**
 * Parade Frontend TypeScript Types
 *
 * Type definitions for all RPC methods, events, and data structures
 * that mirror the Go backend's JSON-RPC interface.
 */

// ============================================================================
// Core Types
// ============================================================================

/** JSON-RPC 2.0 Request */
export interface RPCRequest {
  jsonrpc: "2.0"
  id: number
  method: string
  params?: unknown[]
}

/** JSON-RPC 2.0 Response */
export interface RPCResponse {
  jsonrpc: "2.0"
  id: number
  result?: unknown
  error?: RPCError
}

/** JSON-RPC 2.0 Error */
export interface RPCError {
  code: number
  message: string
  data?: unknown
}

/** JSON-RPC 2.0 Event Push */
export interface RPCEvent {
  jsonrpc: "2.0"
  method: "event"
  params: {
    event: string
    data: unknown
  }
}

// ============================================================================
// Connection State
// ============================================================================

export type ConnectionState = "disconnected" | "connecting" | "connected" | "error"

// ============================================================================
// Identity Types
// ============================================================================

/** Identity check result */
export type HasIdentityResult = boolean

/** Registration request */
export interface RegisterParams {
  password: string
}

/** Login request */
export interface LoginParams {
  password: string
}

// ============================================================================
// Team Types
// ============================================================================

/** Team information */
export interface Team {
  id: string
  name: string
  team_hash: string
  created_at: string
  active: boolean
}

/** Join team request */
export interface JoinTeamParams {
  secret: string
}

/** Join team with name request */
export interface JoinTeamWithNameParams {
  name: string
  secret: string
}

/** Returns team ID */
export type JoinTeamResult = void
export type JoinTeamWithNameResult = string

/** Leave team request */
export interface LeaveTeamParams {
  teamID: string
}

/** Switch team request */
export interface SwitchTeamParams {
  teamID: string
}

/** List teams result */
export type ListTeamsResult = Team[]

/** Get active team result */
export type GetActiveTeamResult = string

/** Get public key result */
export type GetPubKeyResult = string

// ============================================================================
// Conversation Types
// ============================================================================

/** Conversation type */
export type ConversationType = "team" | "private"

/** Conversation information */
export interface Conversation {
  id: string
  team_id: string
  type: ConversationType
  display_name: string
  peer_pubkey: string
  my_pubkey: string
  last_hlc: string
  last_message: string
  last_msg_time: string
  created_at: string
  updated_at: string
}

/** Message information */
export interface Message {
  id: string
  hlc: string
  sender: string
  content: string
  conversation_id: string
  timestamp: string
}

/** Send team chat request */
export interface SendTeamChatParams {
  text: string
}

/** Send private chat request */
export interface SendPrivateChatParams {
  targetUUID: string
  text: string
}

/** List conversations result */
export type ListConversationsResult = Conversation[]

/** Get conversation messages request */
export interface GetConversationMessagesParams {
  convID: string
  limit: number
  offset: number
}

/** Get conversation messages result */
export type GetConversationMessagesResult = Message[]

/** Start private conversation request */
export interface StartPrivateConversationParams {
  peerUUID: string
}

/** Returns conversation ID */
export type StartPrivateConversationResult = string

// ============================================================================
// Network Types
// ============================================================================

/** Peer information */
export interface Peer {
  pubkey: string
  ip: string
}

/** Peer status */
export type PeerStatus = "online" | "offline" | "connecting" | "unknown"

/** Peer with status information */
export interface PeerWithStatus {
  pubkey: string
  ip: string
  status: PeerStatus
  last_heartbeat: string
  last_online: string
}

/** Connect to peer request */
export interface ConnectToPeerParams {
  ipAddress: string
}

/** Phase result */
export interface PhaseResult {
  success: boolean
  label: string
  error: string
}

/** Connect to peer result */
export interface ConnectResult {
  ip: string
  pubkey: string
  phase1: PhaseResult
  phase2: PhaseResult
  phase3Send: PhaseResult
  phase3Recv: PhaseResult
}

/** Get peers result */
export type GetPeersResult = Peer[]

/** Get peers with status result */
export type GetPeersWithStatusResult = PeerWithStatus[]

// ============================================================================
// File Types
// ============================================================================

/** File entry information */
export interface FileEntry {
  name: string
  path: string
  isDirectory: boolean
  size: number
  hash: string
}

/** Share directory request */
export interface ShareDirectoryParams {
  path: string
}

/** Unshare directory request */
export interface UnshareDirectoryParams {
  path: string
}

/** Get directory children request */
export interface GetDirectoryChildrenParams {
  path: string
}

/** Get remote directory children request */
export interface GetRemoteDirectoryChildrenParams {
  targetUUID: string
  path: string
}

/** Start download request */
export interface StartDownloadParams {
  targetUUID: string
  virtualPath: string
  localSavePath: string
}

/** Get default download dir result */
export type GetDefaultDownloadDirResult = string

// ============================================================================
// Share Group Types
// ============================================================================

/** Share group information */
export interface ShareGroup {
  id: string
  team_id: string
  name: string
  created_by: string
  created_at: string
}

/** Share group directory */
export interface ShareGroupDir {
  group_id: string
  dir_path: string
  added_at: string
}

/** Create share group request */
export interface CreateShareGroupParams {
  name: string
}

/** Returns group ID */
export type CreateShareGroupResult = string

/** List share groups result */
export type ListShareGroupsResult = ShareGroup[]

/** Add directory to share group request */
export interface AddDirectoryToShareGroupParams {
  groupID: string
  dirPath: string
}

/** Remove directory from share group request */
export interface RemoveDirectoryFromShareGroupParams {
  groupID: string
  dirPath: string
}

/** Delete share group request */
export interface DeleteShareGroupParams {
  groupID: string
}

/** Get share group dirs request */
export interface GetShareGroupDirsParams {
  groupID: string
}

/** Get share group dirs result */
export type GetShareGroupDirsResult = ShareGroupDir[]

// ============================================================================
// System Types
// ============================================================================

/** Export logs result */
export interface ExportLogsResult {
  content: string
  count: number
}

/** Write log file request */
export interface WriteLogFileParams {
  filePath: string
  content: string
}

// ============================================================================
// Event Payloads
// ============================================================================

/** Log event payload */
export interface LogEventPayload {
  time: string
  level: number
  source: string
  message: string
}

/** Peer event payload (for joined/left events) */
export interface PeerEventPayload {
  peer_uuid: string
  ip_address: string
}

/** New message event payload */
export interface NewMessageEventPayload {
  id: string
  hlc: string
  sender: string
  team_id: string
  conversation_id: string
  content: string
  timestamp: string
}

/** File progress event payload */
export interface FileProgressEventPayload {
  task_id: string
  file_path: string
  transferred: number
  total_size: number
  is_upload: boolean
}

/** File completed event payload */
export type FileCompletedEventPayload = string

/** Peer status event payload */
export interface PeerStatusEventPayload {
  uuid: string
  status: "online" | "offline"
}

/** Conversation updated event payload */
export type ConversationUpdatedEventPayload = null

// ============================================================================
// Event Names
// ============================================================================

export type EventName =
  | "ui_log"
  | "ui_peer_joined"
  | "ui_peer_left"
  | "ui_new_message"
  | "ui_file_progress"
  | "ui_file_completed"
  | "ui_peer_status"
  | "ui_conversation_updated"

/** Event handler type */
export type EventHandler<T = unknown> = (data: T) => void

/** Any event handler */
export type AnyEventHandler = (event: EventName, data: unknown) => void

// ============================================================================
// Error Codes
// ============================================================================

export const ErrorCodes = {
  ParseError: -32700,
  InvalidRequest: -32600,
  MethodNotFound: -32601,
  InvalidParams: -32602,
  InternalError: -32603,
  ServerError: -32000,
  NotLoggedIn: -32001,
  NetworkError: -32002,
  ConnectionFailed: -32003,
  Timeout: -32004,
} as const

// ============================================================================
// ParadeRPC Configuration
// ============================================================================

export interface ParadeRPCOptions {
  /** Unix domain socket path (default: /tmp/parade.sock) */
  udsPath?: string
  /** Request timeout in ms (default: 30000) */
  timeout?: number
  /** Enable debug logging */
  debug?: boolean
}
