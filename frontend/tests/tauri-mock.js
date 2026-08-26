/**
 * Tauri API Mock — injected via page.addInitScript() in Playwright E2E tests.
 *
 * Defines window.__TAURI_INTERNALS__ so the Parade frontend's
 * @tauri-apps/api invoke() and listen() calls work in a standard browser.
 * Routes all RPC calls through WebSocket to the daemon proxy.
 *
 * This is PLAIN JavaScript (not TypeScript) because page.addInitScript()
 * injects raw JS into the browser context.
 */

(function () {
  'use strict';

  // Already injected? Skip
  if (window.__TAURI_INTERNALS__ || window.__TAURI__) {
    return;
  }

  // ── Configuration ──────────────────────────────────────────────────────

  const WS_PORT = window.__PARADE_DAEMON_PROXY_PORT || 9876;
  const WS_URL = `ws://localhost:${WS_PORT}`;
  const RECONNECT_DELAY = 1000;
  const MAX_RECONNECT_ATTEMPTS = 10;

  // ── State ──────────────────────────────────────────────────────────────

  let ws = null;
  let requestId = 1;
  let reconnectAttempts = 0;
  const pendingRequests = {};
  const eventHandlers = {};

  // ── Method Name Mapping (snake_case Tauri cmd → PascalCase daemon method) ─

  const METHOD_MAP = {
    check_has_identity: 'CheckHasIdentity',
    register: 'Register',
    login: 'Login',
    logout: 'Logout',
    join_team: 'JoinTeam',
    join_team_with_name: 'JoinTeamWithName',
    leave_team: 'LeaveTeam',
    switch_team: 'SwitchTeam',
    list_teams: 'ListTeams',
    get_active_team: 'GetActiveTeam',
    send_team_chat: 'SendTeamChat',
    send_private_chat: 'SendPrivateChat',
    get_conversation_messages: 'GetConversationMessages',
    list_conversations: 'ListConversations',
    start_private_conversation: 'StartPrivateConversation',
    get_peers: 'GetPeers',
    get_peers_with_status: 'GetPeersWithStatus',
    connect_to_peer: 'ConnectToPeer',
    list_saved_peers: 'ListSavedPeers',
    save_peer: 'SavePeer',
    remove_peer: 'RemovePeer',
    share_directory: 'ShareDirectory',
    unshare_directory: 'UnshareDirectory',
    get_directory_children: 'GetDirectoryChildren',
    get_remote_directory_children: 'GetRemoteDirectoryChildren',
    start_download: 'StartDownload',
    get_default_download_dir: 'GetDefaultDownloadDir',
    create_share_group: 'CreateShareGroup',
    list_share_groups: 'ListShareGroups',
    add_directory_to_share_group: 'AddDirectoryToShareGroup',
    remove_directory_from_share_group: 'RemoveDirectoryFromShareGroup',
    delete_share_group: 'DeleteShareGroup',
    get_share_group_dirs: 'GetShareGroupDirs',
    get_pub_key: 'GetPubKey',
    export_logs: 'ExportLogs',
    on_foreground: 'OnForeground',
    write_log_file: 'WriteLogFile',
  };

  // ── Param Order Mapping (Tauri named args → daemon positional array) ──

  const PARAM_ORDER = {
    register: ['password'],
    login: ['password'],
    join_team: ['secret'],
    join_team_with_name: ['name', 'secret'],
    leave_team: ['team_id'],
    switch_team: ['team_id'],
    send_team_chat: ['text'],
    send_private_chat: ['target_uuid', 'text'],
    get_conversation_messages: ['conv_id', 'limit', 'offset'],
    start_private_conversation: ['peer_uuid'],
    connect_to_peer: ['ip_address'],
    save_peer: ['ip_address'],
    remove_peer: ['ip_address'],
    share_directory: ['path'],
    unshare_directory: ['path'],
    get_directory_children: ['path'],
    get_remote_directory_children: ['target_uuid', 'path'],
    start_download: ['target_uuid', 'virtual_path', 'local_save_path'],
    create_share_group: ['name'],
    add_directory_to_share_group: ['group_id', 'dir_path'],
    remove_directory_from_share_group: ['group_id', 'dir_path'],
    delete_share_group: ['group_id'],
    get_share_group_dirs: ['group_id'],
    write_log_file: ['file_path', 'content'],
  };

  // ── WebSocket Connection ───────────────────────────────────────────────

  function connect() {
    if (ws && ws.readyState === WebSocket.OPEN) return;

    try {
      ws = new WebSocket(WS_URL);
    } catch (err) {
      console.error('[tauri-mock] WebSocket construction failed:', err);
      scheduleReconnect();
      return;
    }

    ws.onopen = function () {
      console.log('[tauri-mock] WebSocket connected to ' + WS_URL);
      reconnectAttempts = 0;
      window.__PARADE_RPC_READY = true;

      // Resolve any pending listen registrations
      if (window.__tauriMockReadyResolve) {
        window.__tauriMockReadyResolve();
      }
    };

    ws.onmessage = function (event) {
      try {
        handleMessage(event.data);
      } catch (err) {
        console.error('[tauri-mock] Message handling error:', err);
      }
    };

    ws.onclose = function () {
      console.warn('[tauri-mock] WebSocket disconnected');
      window.__PARADE_RPC_READY = false;
      scheduleReconnect();
    };

    ws.onerror = function (err) {
      console.error('[tauri-mock] WebSocket error:', err);
    };
  }

  function scheduleReconnect() {
    if (reconnectAttempts >= MAX_RECONNECT_ATTEMPTS) {
      console.error('[tauri-mock] Max reconnect attempts reached');
      return;
    }
    reconnectAttempts++;
    console.log('[tauri-mock] Reconnecting in ' + RECONNECT_DELAY + 'ms (attempt ' + reconnectAttempts + ')');
    setTimeout(connect, RECONNECT_DELAY);
  }

  function ensureConnected() {
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      connect();
    }
  }

  function waitForConnection(timeoutMs) {
    return new Promise(function (resolve, reject) {
      if (ws && ws.readyState === WebSocket.OPEN) {
        resolve();
        return;
      }
      var timer = setTimeout(function () {
        reject(new Error('WebSocket connection timed out after ' + timeoutMs + 'ms'));
      }, timeoutMs || 15000);
      function check() {
        if (ws && ws.readyState === WebSocket.OPEN) {
          clearTimeout(timer);
          resolve();
        } else {
          setTimeout(check, 50);
        }
      }
      check();
    });
  }

  // ── Message Handling ───────────────────────────────────────────────────

  function handleMessage(data) {
    var msg;
    try {
      msg = JSON.parse(data);
    } catch (e) {
      return;
    }

    // Event broadcast (no id, method === "event")
    if (msg.method === 'event' && msg.id === undefined && msg.params) {
      var eventName = msg.params.event;
      var eventData = msg.params.data;
      var handlers = eventHandlers[eventName];
      if (handlers) {
        for (var i = 0; i < handlers.length; i++) {
          try {
            // Tauri callback format: call the stored _tauri_cb_ function
            // with { payload: data } shaped argument
            if (typeof handlers[i] === 'function') {
              handlers[i]({ payload: eventData });
            }
          } catch (err) {
            console.error('[tauri-mock] Event handler error for ' + eventName + ':', err);
          }
        }
      }
      return;
    }

    // Response to a request
    if (msg.id !== undefined && msg.id !== null) {
      var pending = pendingRequests[msg.id];
      if (pending) {
        delete pendingRequests[msg.id];
        if (msg.error) {
          pending.reject(msg.error.message || 'RPC error ' + msg.error.code);
        } else {
          pending.resolve(msg.result);
        }
      }
    }
  }

  // ── invoke() implementation ────────────────────────────────────────────

  function invoke(cmd, args) {
    // Handle write_log_file locally (stub — browser can't write to filesystem)
    if (cmd === 'write_log_file') {
      console.log('[tauri-mock] write_log_file (stubbed):', args);
      return Promise.resolve(null);
    }

    // ── Tauri plugin:event|listen — wraps our custom listen() ────────────
    if (cmd === 'plugin:event|listen') {
      var eventName = args.event;
      var callbackId = args.handler; // transformCallback returns numeric ID
      var handler = window['_tauri_cb_' + callbackId];
      if (!handler) {
        return Promise.reject('transformCallback handler not found for id ' + callbackId);
      }
      // Register with our event system
      if (!eventHandlers[eventName]) {
        eventHandlers[eventName] = [];
      }
      eventHandlers[eventName].push(handler);
      ensureConnected();
      // Return a fake event ID
      var fakeId = (window.__tauriEventId || 0) + 1;
      window.__tauriEventId = fakeId;
      return Promise.resolve(fakeId);
    }

    // ── Tauri plugin:event|unlisten — removes listener ───────────────────
    if (cmd === 'plugin:event|unlisten') {
      // Just a no-op — our listeners are cleaned up when page reloads
      return Promise.resolve(null);
    }

    // ── Handle Tauri internal commands with stub responses ───────────────
    if (cmd.startsWith('plugin:')) {
      console.log('[tauri-mock] Stubbing Tauri plugin command:', cmd);
      return Promise.resolve(null);
    }

    ensureConnected();

    return waitForConnection(15000).then(function () {
      return new Promise(function (resolve, reject) {
        var daemonMethod = METHOD_MAP[cmd];
        if (!daemonMethod) {
          reject('Unknown command: ' + cmd);
          return;
        }

        var id = requestId++;

        // Convert named args to positional array
        var params = null;
        var order = PARAM_ORDER[cmd];
        if (order && args) {
          params = order.map(function (key) { return args[key]; });
        }

        var request = {
          jsonrpc: '2.0',
          id: id,
          method: daemonMethod,
          params: params,
        };

        pendingRequests[id] = { resolve: resolve, reject: reject };

        try {
          ws.send(JSON.stringify(request));
        } catch (err) {
          reject('WebSocket send failed: ' + err.message);
          delete pendingRequests[id];
        }
      });
    });
  }

  // ── listen() implementation ────────────────────────────────────────────

  function listen(eventName, handler) {
    if (!eventHandlers[eventName]) {
      eventHandlers[eventName] = [];
    }
    eventHandlers[eventName].push(handler);

    // Ensure WebSocket is connected so we receive events
    ensureConnected();

    // Return unlisten function
    return Promise.resolve(function () {
      var handlers = eventHandlers[eventName];
      if (handlers) {
        var idx = handlers.indexOf(handler);
        if (idx >= 0) {
          handlers.splice(idx, 1);
        }
        if (handlers.length === 0) {
          delete eventHandlers[eventName];
        }
      }
    });
  }

  // ── Inject __TAURI_INTERNALS__ ─────────────────────────────────────────

  window.__TAURI_INTERNALS__ = {
    invoke: invoke,
    listen: listen,
    convertFileSrc: function (filePath) { return filePath; },
    transformCallback: function (callback, once) {
      var id = window.__tauriCallbackId || 0;
      window.__tauriCallbackId = id + 1;
      window['_tauri_cb_' + id] = callback;
      return id;
    },
  };

  // Also set __TAURI__ for any code that checks it
  window.__TAURI__ = {};

  // Tauri event plugin internals (needed by _unlisten)
  window.__TAURI_EVENT_PLUGIN_INTERNALS__ = {
    unregisterListener: function (event, eventId) {
      // No-op in mock — listeners are cleaned up when page reloads
    },
  };

  // Ready promise for code that waits for Tauri initialization

  // Ready promise for code that waits for Tauri initialization
  window.__tauriMockReady = new Promise(function (resolve) {
    window.__tauriMockReadyResolve = resolve;
  });
  window.__PARADE_RPC_READY = false;

  // Try connecting immediately
  connect();

  console.log('[tauri-mock] Injected — WebSocket target: ' + WS_URL);
})();
