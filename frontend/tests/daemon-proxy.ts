/**
 * Daemon Proxy — bridges Parade Go daemon (stdio JSON-RPC) to WebSocket clients.
 *
 * Used by Playwright E2E tests to let browser-based frontend code communicate
 * with the real Go daemon without Tauri's IPC bridge.
 *
 * Architecture:
 *   Browser (via tauri-mock.js) ←WebSocket→ DaemonProxy ←stdio→ parade.exe
 */

import { spawn, ChildProcess } from 'child_process';
import { WebSocketServer, WebSocket } from 'ws';
import { createInterface } from 'readline';
import { fileURLToPath } from 'url';
import * as path from 'path';
import * as fs from 'fs';
import * as os from 'os';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// ── Types ───────────────────────────────────────────────────────────────────

interface JSONRPCRequest {
  jsonrpc: '2.0';
  id: number;
  method: string;
  params?: unknown[];
}

interface JSONRPCResponse {
  jsonrpc: '2.0';
  id?: number;
  result?: unknown;
  error?: { code: number; message: string };
  method?: string;
  params?: { event: string; data: unknown };
}

// ── State ───────────────────────────────────────────────────────────────────

let daemonProcess: ChildProcess | null = null;
let wss: WebSocketServer | null = null;
let requestId = 1;
const pendingRequests = new Map<number, WebSocket>();
const connectedClients = new Set<WebSocket>();
let dataDir = '';
let proxyPort = 0;

// ── Daemon Management ───────────────────────────────────────────────────────

function getDaemonPath(): string {
  // __dirname = D:\Parade\frontend\tests
  // workspaceRoot = D:\Parade (go up 2 levels from tests/)
  const workspaceRoot = path.resolve(__dirname, '..', '..');
  const candidates = [
    path.join(workspaceRoot, 'parade.exe'),
    path.join(workspaceRoot, 'parade'),
  ];
  for (const c of candidates) {
    // Must be a file, not a directory (Windows case-insensitivity)
    if (fs.existsSync(c) && fs.statSync(c).isFile()) return c;
  }
  throw new Error(
    `Cannot find parade daemon binary. Tried: ${candidates.join(', ')}`
  );
}

function startDaemon(dataDir: string): ChildProcess {
  const binPath = getDaemonPath();
  console.log(`[proxy] Starting daemon: ${binPath}`);
  console.log(`[proxy] Data directory: ${dataDir}`);

  const proc = spawn(binPath, [
    'daemon',
    '--debug',
    '--data-dir', dataDir,
    '--no-mdns',
  ], {
    stdio: ['pipe', 'pipe', 'pipe'],
    windowsHide: true,
  });

  proc.stderr?.on('data', (chunk: Buffer) => {
    const lines = chunk.toString().split('\n').filter(Boolean);
    for (const line of lines) {
      console.log(`[daemon] ${line}`);
    }
  });

  proc.on('error', (err) => {
    console.error(`[proxy] Daemon spawn error: ${err.message}`);
  });

  proc.on('exit', (code, signal) => {
    console.log(`[proxy] Daemon exited (code=${code}, signal=${signal})`);
  });

  return proc;
}

// ── WebSocket Server ────────────────────────────────────────────────────────

function startWebSocketServer(port: number): WebSocketServer {
  const server = new WebSocketServer({ port });

  server.on('listening', () => {
    console.log(`[proxy] WebSocket server listening on ws://localhost:${port}`);
  });

  server.on('connection', (ws) => {
    console.log(`[proxy] Client connected (total: ${connectedClients.size + 1})`);
    connectedClients.add(ws);

    ws.on('message', (data) => {
      handleClientMessage(ws, data.toString());
    });

    ws.on('close', () => {
      connectedClients.delete(ws);
      console.log(`[proxy] Client disconnected (remaining: ${connectedClients.size})`);
    });

    ws.on('error', (err) => {
      console.error(`[proxy] WebSocket client error: ${err.message}`);
      connectedClients.delete(ws);
    });
  });

  server.on('error', (err) => {
    console.error(`[proxy] WebSocket server error: ${err.message}`);
  });

  return server;
}

// ── Message Handling ────────────────────────────────────────────────────────

function handleClientMessage(ws: WebSocket, raw: string): void {
  if (!daemonProcess?.stdin) {
    ws.send(JSON.stringify({
      jsonrpc: '2.0',
      id: null,
      error: { code: -32000, message: 'Daemon not running' },
    }));
    return;
  }

  let req: JSONRPCRequest;
  try {
    req = JSON.parse(raw);
  } catch {
    ws.send(JSON.stringify({
      jsonrpc: '2.0',
      id: null,
      error: { code: -32700, message: 'Parse error' },
    }));
    return;
  }

  // Allow client-specified IDs; generate if missing
  const id = req.id ?? requestId++;
  if (!req.id) {
    req.id = id;
  }

  // Track this request for response routing
  pendingRequests.set(id, ws);

  // Forward to daemon stdin
  const payload = JSON.stringify(req) + '\n';
  daemonProcess.stdin.write(payload);
}

function handleDaemonLine(raw: string): void {
  let msg: JSONRPCResponse;
  try {
    msg = JSON.parse(raw);
  } catch {
    console.warn(`[proxy] Failed to parse daemon output: ${raw.slice(0, 100)}`);
    return;
  }

  // Is this an event (no id, method === "event")?
  if (msg.method === 'event' && msg.id === undefined) {
    const eventData = JSON.stringify(msg);
    for (const client of connectedClients) {
      if (client.readyState === WebSocket.OPEN) {
        client.send(eventData);
      }
    }
    return;
  }

  // Is this a response to a request?
  if (msg.id !== undefined && msg.id !== null) {
    const client = pendingRequests.get(msg.id);
    if (client && client.readyState === WebSocket.OPEN) {
      client.send(raw);
    }
    pendingRequests.delete(msg.id);
    return;
  }

  // Unmatched output — broadcast to all
  for (const client of connectedClients) {
    if (client.readyState === WebSocket.OPEN) {
      client.send(raw);
    }
  }
}

function setupDaemonStdio(proc: ChildProcess): void {
  const rl = createInterface({
    input: proc.stdout!,
    crlfDelay: Infinity,
  });

  rl.on('line', (line: string) => {
    handleDaemonLine(line);
  });

  rl.on('close', () => {
    console.log('[proxy] Daemon stdout closed');
  });
}

// ── Health Check ────────────────────────────────────────────────────────────

function healthCheck(): Promise<void> {
  return new Promise((resolve, reject) => {
    if (!daemonProcess?.stdin) {
      reject(new Error('Daemon process not started'));
      return;
    }

    const timeout = setTimeout(() => {
      reject(new Error('Daemon health check timed out (waited 30s)'));
    }, 30_000);

    // Use a fake WebSocket-like object to capture the response
    const fakeWs = {
      readyState: WebSocket.OPEN,
      send: (data: string) => {
        clearTimeout(timeout);
        try {
          const resp = JSON.parse(data);
          if (resp.result === true || resp.result === false) {
            console.log(`[proxy] Health check passed (hasIdentity=${resp.result})`);
            resolve();
          }
        } catch {
          console.log(`[proxy] Health check response: ${data}`);
          resolve();
        }
      },
    } as unknown as WebSocket;

    pendingRequests.set(0, fakeWs);
    daemonProcess.stdin.write(
      '{"jsonrpc":"2.0","id":0,"method":"CheckHasIdentity","params":null}\n'
    );
  });
}

// ── Public API ──────────────────────────────────────────────────────────────

export async function startDaemonProxy(
  testDataDir: string,
  port: number = 9876
): Promise<{ url: string; port: number }> {
  if (daemonProcess) {
    console.log('[proxy] Daemon proxy already running');
    return { url: `ws://localhost:${port}`, port };
  }

  // Ensure data directory exists
  fs.mkdirSync(testDataDir, { recursive: true });

  dataDir = testDataDir;
  proxyPort = port;

  // Start daemon
  daemonProcess = startDaemon(dataDir);

  // Set up stdio forwarding
  setupDaemonStdio(daemonProcess);

  // Start WebSocket server
  wss = startWebSocketServer(port);

  // Wait for daemon to be ready
  // Give it a moment to start before health checking
  await new Promise(r => setTimeout(r, 500));
  await healthCheck();

  console.log(`[proxy] Daemon proxy ready at ws://localhost:${port}`);
  return { url: `ws://localhost:${port}`, port };
}

export async function stopDaemonProxy(): Promise<void> {
  console.log('[proxy] Stopping daemon proxy...');

  // Close all WebSocket connections
  if (wss) {
    for (const client of connectedClients) {
      client.close();
    }
    connectedClients.clear();
    await new Promise<void>((resolve) => {
      wss!.close(() => resolve());
    });
    wss = null;
  }

  // Kill daemon
  if (daemonProcess) {
    // Send EOF to stdin (triggers clean shutdown)
    if (daemonProcess.stdin) {
      daemonProcess.stdin.end();
    }

    // Wait for graceful exit, force kill after 5s
    const killTimeout = setTimeout(() => {
      if (daemonProcess) {
        console.log('[proxy] Force killing daemon...');
        daemonProcess.kill('SIGKILL');
      }
    }, 5_000);

    await new Promise<void>((resolve) => {
      daemonProcess!.on('exit', () => {
        clearTimeout(killTimeout);
        resolve();
      });
      // If already exited, resolve immediately
      if (daemonProcess!.exitCode !== null) {
        clearTimeout(killTimeout);
        resolve();
      }
    });

    daemonProcess = null;
  }

  // Clean up data directory
  if (dataDir) {
    try {
      fs.rmSync(dataDir, { recursive: true, force: true });
      console.log(`[proxy] Cleaned up data directory: ${dataDir}`);
    } catch (err) {
      console.warn(`[proxy] Failed to clean up data dir: ${err}`);
    }
  }

  pendingRequests.clear();
  console.log('[proxy] Daemon proxy stopped');
}

export function getProxyPort(): number {
  return proxyPort;
}
