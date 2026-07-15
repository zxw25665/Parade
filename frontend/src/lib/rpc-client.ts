import { invoke } from "@tauri-apps/api/core";
import { listen, UnlistenFn } from "@tauri-apps/api/event";
import type {
  ConnectionState,
  EventHandler,
  AnyEventHandler,
  EventName,
  ParadeRPCOptions,
  Team,
  Conversation,
  Message,
  Peer,
  PeerWithStatus,
  FileEntry,
  ShareGroup,
  ShareGroupDir,
  ConnectResult,
  LogEventPayload,
  PeerEventPayload,
  NewMessageEventPayload,
  FileProgressEventPayload,
  FileCompletedEventPayload,
  PeerStatusEventPayload,
  ConversationUpdatedEventPayload,
} from "./types";

const DEFAULT_TIMEOUT = 30000;

export class ParadeRPC {
  private eventHandlers = new Map<EventName, Set<EventHandler>>();
  private anyEventHandlers: Set<AnyEventHandler> = new Set();
  private unlistenFns: UnlistenFn[] = [];
  private connectionState: ConnectionState = "disconnected";
  private stateChangeListeners: Set<(state: ConnectionState) => void> = new Set();
  private timeout: number;
  private debug: boolean;

  constructor(options: ParadeRPCOptions = {}) {
    this.timeout = options.timeout ?? DEFAULT_TIMEOUT;
    this.debug = options.debug ?? false;
  }

  private log(...args: unknown[]): void {
    if (this.debug) {
      console.debug("[ParadeRPC]", ...args);
    }
  }

  private setState(state: ConnectionState): void {
    this.connectionState = state;
    this.stateChangeListeners.forEach((listener) => listener(state));
  }

  onStateChange(handler: (state: ConnectionState) => void): () => void {
    this.stateChangeListeners.add(handler);
    handler(this.connectionState);
    return () => this.stateChangeListeners.delete(handler);
  }

  getState(): ConnectionState {
    return this.connectionState;
  }

  async connect(): Promise<void> {
    if (this.connectionState === "connected") {
      return;
    }

    this.setState("connecting");
    this.log("Connecting to Parade daemon...");

    try {
      await this.setupEventListeners();
      this.setState("connected");
      this.log("Connected successfully");
    } catch (error) {
      this.setState("error");
      throw new Error(`Failed to connect: ${error}`);
    }
  }

  private async setupEventListeners(): Promise<void> {
    const eventNames: EventName[] = [
      "ui_log",
      "ui_peer_joined",
      "ui_peer_left",
      "ui_new_message",
      "ui_file_progress",
      "ui_file_completed",
      "ui_peer_status",
      "ui_conversation_updated",
    ];

    for (const eventName of eventNames) {
      const unlisten = await listen<unknown>(eventName, (event) => {
        this.log("Event received:", eventName, event.payload);
        this.dispatchEvent(eventName, event.payload);
      });
      this.unlistenFns.push(unlisten);
    }
  }

  private dispatchEvent(event: EventName, data: unknown): void {
    const handlers = this.eventHandlers.get(event);
    if (handlers) {
      handlers.forEach((handler) => {
        try {
          handler(data);
        } catch (error) {
          console.error(`Event handler error for ${event}:`, error);
        }
      });
    }

    this.anyEventHandlers.forEach((handler) => {
      try {
        handler(event, data);
      } catch (error) {
        console.error("AnyEvent handler error:", error);
      }
    });
  }

  disconnect(): void {
    this.log("Disconnecting...");

    this.unlistenFns.forEach((fn) => fn());
    this.unlistenFns = [];

    this.eventHandlers.clear();
    this.anyEventHandlers.clear();

    this.setState("disconnected");
  }

  async call<T>(invokeName: string, args?: Record<string, unknown>): Promise<T> {
    if (this.connectionState !== "connected") {
      throw new Error(`Not connected (state: ${this.connectionState})`);
    }

    this.log("Invoking:", invokeName, args);

    return invoke<T>(invokeName, args);
  }

  on(event: EventName, handler: EventHandler): () => void {
    if (!this.eventHandlers.has(event)) {
      this.eventHandlers.set(event, new Set());
    }
    this.eventHandlers.get(event)!.add(handler);
    return () => this.off(event, handler);
  }

  off(event: EventName, handler: EventHandler): void {
    const handlers = this.eventHandlers.get(event);
    if (handlers) {
      handlers.delete(handler);
      if (handlers.size === 0) {
        this.eventHandlers.delete(event);
      }
    }
  }

  onAny(handler: AnyEventHandler): () => void {
    this.anyEventHandlers.add(handler);
    return () => this.anyEventHandlers.delete(handler);
  }

  // =========================================================================
  // Identity Methods
  // =========================================================================

  async checkHasIdentity(): Promise<boolean> {
    return this.call<boolean>("check_has_identity");
  }

  async register(password: string): Promise<void> {
    await this.call<void>("register", { password });
  }

  async login(password: string): Promise<void> {
    await this.call<void>("login", { password });
  }

  // =========================================================================
  // Team Methods
  // =========================================================================

  async joinTeam(secret: string): Promise<void> {
    await this.call<void>("join_team", { secret });
  }

  async joinTeamWithName(name: string, secret: string): Promise<string> {
    return this.call<string>("join_team_with_name", { name, secret });
  }

  async leaveTeam(teamID: string): Promise<void> {
    await this.call<void>("leave_team", { team_id: teamID });
  }

  async switchTeam(teamID: string): Promise<void> {
    await this.call<void>("switch_team", { team_id: teamID });
  }

  async listTeams(): Promise<Team[]> {
    return this.call<Team[]>("list_teams");
  }

  async getActiveTeam(): Promise<string> {
    return this.call<string>("get_active_team");
  }

  async getPubKey(): Promise<string> {
    return this.call<string>("get_pub_key");
  }

  // =========================================================================
  // Chat Methods
  // =========================================================================

  async sendTeamChat(text: string): Promise<void> {
    await this.call<void>("send_team_chat", { text });
  }

  async sendPrivateChat(targetUUID: string, text: string): Promise<void> {
    await this.call<void>("send_private_chat", { target_uuid: targetUUID, text });
  }

  async listConversations(): Promise<Conversation[]> {
    return this.call<Conversation[]>("list_conversations");
  }

  async getConversationMessages(
    convID: string,
    limit: number,
    offset: number
  ): Promise<Message[]> {
    return this.call<Message[]>("get_conversation_messages", {
      conv_id: convID,
      limit,
      offset,
    });
  }

  async startPrivateConversation(peerUUID: string): Promise<string> {
    return this.call<string>("start_private_conversation", { peer_uuid: peerUUID });
  }

  // =========================================================================
  // Network Methods
  // =========================================================================

  async getPeers(): Promise<Peer[]> {
    return this.call<Peer[]>("get_peers");
  }

  async getPeersWithStatus(): Promise<PeerWithStatus[]> {
    return this.call<PeerWithStatus[]>("get_peers_with_status");
  }

  async connectToPeer(ipAddress: string): Promise<ConnectResult> {
    return this.call<ConnectResult>("connect_to_peer", { ip_address: ipAddress });
  }

  async listSavedPeers(): Promise<string[]> {
    return this.call<string[]>("list_saved_peers");
  }

  async savePeer(ipAddress: string): Promise<void> {
    await this.call<void>("save_peer", { ip_address: ipAddress });
  }

  async removePeer(ipAddress: string): Promise<void> {
    await this.call<void>("remove_peer", { ip_address: ipAddress });
  }

  async onForeground(): Promise<void> {
    await this.call<void>("on_foreground");
  }

  // =========================================================================
  // File Methods
  // =========================================================================

  async shareDirectory(path: string): Promise<void> {
    await this.call<void>("share_directory", { path });
  }

  async unshareDirectory(path: string): Promise<void> {
    await this.call<void>("unshare_directory", { path });
  }

  async getDirectoryChildren(path: string): Promise<FileEntry[]> {
    return this.call<FileEntry[]>("get_directory_children", { path });
  }

  async getRemoteDirectoryChildren(
    targetUUID: string,
    path: string
  ): Promise<FileEntry[]> {
    return this.call<FileEntry[]>("get_remote_directory_children", {
      target_uuid: targetUUID,
      path,
    });
  }

  async startDownload(
    targetUUID: string,
    virtualPath: string,
    localSavePath: string
  ): Promise<void> {
    await this.call<void>("start_download", {
      target_uuid: targetUUID,
      virtual_path: virtualPath,
      local_save_path: localSavePath,
    });
  }

  async getDefaultDownloadDir(): Promise<string> {
    return this.call<string>("get_default_download_dir");
  }

  // =========================================================================
  // Share Group Methods
  // =========================================================================

  async createShareGroup(name: string): Promise<string> {
    return this.call<string>("create_share_group", { name });
  }

  async listShareGroups(): Promise<ShareGroup[]> {
    return this.call<ShareGroup[]>("list_share_groups");
  }

  async addDirectoryToShareGroup(groupID: string, dirPath: string): Promise<void> {
    await this.call<void>("add_directory_to_share_group", {
      group_id: groupID,
      dir_path: dirPath,
    });
  }

  async removeDirectoryFromShareGroup(groupID: string, dirPath: string): Promise<void> {
    await this.call<void>("remove_directory_from_share_group", {
      group_id: groupID,
      dir_path: dirPath,
    });
  }

  async deleteShareGroup(groupID: string): Promise<void> {
    await this.call<void>("delete_share_group", { group_id: groupID });
  }

  async getShareGroupDirs(groupID: string): Promise<ShareGroupDir[]> {
    return this.call<ShareGroupDir[]>("get_share_group_dirs", { group_id: groupID });
  }

  // =========================================================================
  // System Methods
  // =========================================================================

  async exportLogs(): Promise<{ content: string; count: number }> {
    return this.call<{ content: string; count: number }>("export_logs");
  }

  async writeLogFile(filePath: string, content: string): Promise<void> {
    await this.call<void>("write_log_file", { file_path: filePath, content });
  }
}

export function createTypedEventHandlers(rpc: ParadeRPC) {
  return {
    onLog(handler: EventHandler<LogEventPayload>): () => void {
      return rpc.on("ui_log", handler as EventHandler);
    },

    onPeerJoined(handler: EventHandler<PeerEventPayload>): () => void {
      return rpc.on("ui_peer_joined", handler as EventHandler);
    },

    onPeerLeft(handler: EventHandler<PeerEventPayload>): () => void {
      return rpc.on("ui_peer_left", handler as EventHandler);
    },

    onNewMessage(handler: EventHandler<NewMessageEventPayload>): () => void {
      return rpc.on("ui_new_message", handler as EventHandler);
    },

    onFileProgress(handler: EventHandler<FileProgressEventPayload>): () => void {
      return rpc.on("ui_file_progress", handler as EventHandler);
    },

    onFileCompleted(handler: EventHandler<FileCompletedEventPayload>): () => void {
      return rpc.on("ui_file_completed", handler as EventHandler);
    },

    onPeerStatus(handler: EventHandler<PeerStatusEventPayload>): () => void {
      return rpc.on("ui_peer_status", handler as EventHandler);
    },

    onConversationUpdated(handler: EventHandler<ConversationUpdatedEventPayload>): () => void {
      return rpc.on("ui_conversation_updated", handler as EventHandler);
    },
  };
}

export type TypedEventHandlers = ReturnType<typeof createTypedEventHandlers>;

export {
  type Team,
  type Conversation,
  type Message,
  type Peer,
  type PeerWithStatus,
  type FileEntry,
  type ShareGroup,
  type ShareGroupDir,
  type ConnectResult,
  type ConnectionState,
  type EventName,
  type LogEventPayload,
  type PeerEventPayload,
  type NewMessageEventPayload,
  type FileProgressEventPayload,
  type FileCompletedEventPayload,
  type PeerStatusEventPayload,
  type ConversationUpdatedEventPayload,
};
