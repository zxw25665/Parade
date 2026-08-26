"""Parade daemon stdio JSON-RPC transport harness (stdlib only).

Spawns a real `parade daemon` process and speaks newline-delimited JSON-RPC
2.0 over its stdin/stdout pipes, mirroring the production frontend contract
(internal/app/stdio_server.go). Facts this harness relies on:

- Requests carry `params` in ARRAY form (the reflection registry parses
  `params` as []json.RawMessage).
- Responses echo the request id and may arrive OUT OF ORDER (each line is
  dispatched in its own goroutine) — the client correlates by id.
- Events are id-less notifications: {"method":"event","params":{"event":...,"data":...}}.
- stdout must contain ONLY JSON-RPC lines once the IPC server starts; the
  single documented pre-startup pollution is the shared-directories warning
  (cmd/parade/daemon/daemon.go).
- stdin EOF triggers a graceful daemon exit with code 0 (daemon.go select on
  ipcSrv.Exited()). Windows fallback: taskkill /PID <pid> /T /F.

Windows-first, Python 3.12+, zero third-party dependencies.
"""

from __future__ import annotations

import io
import json
import os
import queue
import socket
import subprocess
import sys
import threading
import time
from pathlib import Path

# Env vars the daemon reads via ApplyEnvOverrides / config loading. Each node
# must run with a clean, per-node environment so no inherited value leaks
# between nodes or from the developer shell.
PARADE_ENV_KEYS = (
    "PARADE_DATA_DIR",
    "PARADE_PORT",
    "PARADE_LISTEN",
    "PARADE_HEADLESS",
    "PARADE_DEBUG",
    "PARADE_PRODUCTION",
    "PARADE_MDNS_ENABLED",
    "PARADE_AUTO_RECONNECT",
    "PARADE_LOG_LEVEL",
)

# The only known pre-startup stdout pollution (daemon.go:92, file engine load
# failure). Recorded as evidence; not treated as a protocol violation.
KNOWN_PRE_STARTUP_STDOUT = "warning: failed to load shared directories"

# Default per-call timeout, aligned with the design document (10s).
DEFAULT_CALL_TIMEOUT = 10.0

# Bounded capture: per-stream log cap and in-memory tail kept for evidence.
LOG_CAP_BYTES = 4 * 1024 * 1024
TAIL_LINES = 2000


class RpcError(Exception):
    """A JSON-RPC error response (code != 0)."""

    def __init__(self, code, message):
        super().__init__(f"JSON-RPC error {code}: {message}")
        self.code = code
        self.message = message


class RpcTimeout(Exception):
    pass


class NodeExited(Exception):
    """The daemon process died while a call was in flight."""

    def __init__(self, exit_code):
        super().__init__(f"daemon process exited with code {exit_code}")
        self.exit_code = exit_code


class Node:
    """One real `parade daemon` process with a multiplexed stdio RPC client."""

    def __init__(
        self,
        name: str,
        binary: str,
        data_dir: Path,
        p2p_port: int,
        workdir: Path,
        artifacts: Path,
        listen: str = "127.0.0.1",
        mdns: bool = True,
        parent_env: dict | None = None,
    ):
        self.name = name
        self.p2p_port = p2p_port
        self.identify_port = p2p_port + 1
        self.data_dir = Path(data_dir)
        self.exit_code: int | None = None
        self.kill_required = False
        self.contamination: list[dict] = []  # non-JSON stdout lines (evidence)

        env = dict(os.environ if parent_env is None else parent_env)
        for key in PARADE_ENV_KEYS:
            env.pop(key, None)
        env["PARADE_DATA_DIR"] = str(self.data_dir)

        cmd = [
            str(binary),
            "daemon",
            "--debug",  # skip the per-data-dir instance lock
            "--listen", listen,
            "--port", str(p2p_port),  # explicit port, NEVER 0
            "--data-dir", str(self.data_dir),
            "--mdns" if mdns else "--no-mdns",
        ]
        self.cmd = cmd
        self.proc = subprocess.Popen(
            cmd,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            cwd=str(workdir),
            env=env,
        )

        self._id_lock = threading.Lock()
        self._next_id = 0
        self._pending: dict[int, queue.Queue] = {}
        self._pending_lock = threading.Lock()
        self._subs: dict[str, list] = {}
        self._subs_lock = threading.Lock()
        self._event_counts: dict[str, int] = {}
        self._write_lock = threading.Lock()
        self._dead = False
        self._dead_code: int | None = None

        self._stdout_fh = (artifacts / f"{name}-stdout.log").open("a", encoding="utf-8", errors="replace")
        self._stderr_fh = (artifacts / f"{name}-stderr.log").open("a", encoding="utf-8", errors="replace")
        self._stdout_bytes = 0
        self._stderr_bytes = 0
        self._stdout_truncated = False
        self._stderr_truncated = False
        self._stdout_tail: list[str] = []
        self._stderr_tail: list[str] = []
        self._artifacts = artifacts

        threading.Thread(target=self._read_loop, name=f"{name}-stdout", daemon=True).start()
        threading.Thread(target=self._stderr_loop, name=f"{name}-stderr", daemon=True).start()

    # ------------------------------------------------------------------ state

    @property
    def pid(self) -> int:
        return self.proc.pid

    def alive(self) -> bool:
        return self.proc.poll() is None

    def _mark_dead(self, code: int) -> None:
        self._dead = True
        self._dead_code = code
        with self._pending_lock:
            pending = list(self._pending.values())
            self._pending.clear()
        for q in pending:
            q.put_nowait({"__exited__": code})

    # ------------------------------------------------------------- line pump

    def _record_line(self, is_stdout: bool, line: str) -> None:
        fh = self._stdout_fh if is_stdout else self._stderr_fh
        size_ref = self._stdout_bytes if is_stdout else self._stderr_bytes
        if size_ref < LOG_CAP_BYTES:
            fh.write(line + "\n")
            if is_stdout:
                self._stdout_bytes = size_ref + len(line) + 1
            else:
                self._stderr_bytes = size_ref + len(line) + 1
        else:
            if is_stdout:
                self._stdout_truncated = True
                self._stdout_tail.append(line)
                if len(self._stdout_tail) > TAIL_LINES:
                    self._stdout_tail.pop(0)
            else:
                self._stderr_truncated = True
                self._stderr_tail.append(line)
                if len(self._stderr_tail) > TAIL_LINES:
                    self._stderr_tail.pop(0)

    def _finalize_log(self, fh, truncated, tail):
        if fh is None:
            return
        if truncated:
            fh.write(f"... (log truncated at {LOG_CAP_BYTES} bytes; tail follows)\n")
            for line in tail:
                fh.write(line + "\n")
        fh.close()

    def _read_loop(self) -> None:
        stream = io.TextIOWrapper(_ensure_pipe(self.proc.stdout), encoding="utf-8", errors="replace", newline="\n")
        try:
            for raw in stream:
                line = raw.rstrip("\r\n")
                if line:
                    self._handle_line(line)
        finally:
            code = self.proc.poll()
            self._mark_dead(0 if code is None else code)
            try:
                stream.close()
            except Exception:
                pass
            self._finalize_log(self._stdout_fh, self._stdout_truncated, self._stdout_tail)

    def _stderr_loop(self) -> None:
        stream = io.TextIOWrapper(_ensure_pipe(self.proc.stderr), encoding="utf-8", errors="replace", newline="\n")
        try:
            for raw in stream:
                line = raw.rstrip("\r\n")
                if line:
                    self._record_line(False, line)
        finally:
            try:
                stream.close()
            except Exception:
                pass
            self._finalize_log(self._stderr_fh, self._stderr_truncated, self._stderr_tail)

    def _handle_line(self, line: str) -> None:
        try:
            msg = json.loads(line)
        except (json.JSONDecodeError, UnicodeDecodeError):
            self.contamination.append(
                {"line": line, "known_pollution": line.startswith(KNOWN_PRE_STARTUP_STDOUT)}
            )
            return

        if not isinstance(msg, dict):
            self.contamination.append({"line": line, "known_pollution": False})
            return

        if "id" in msg and ("result" in msg or "error" in msg):
            rid = msg.get("id")
            if isinstance(rid, int):
                with self._pending_lock:
                    q = self._pending.pop(rid, None)
                if q is not None:
                    q.put_nowait(msg)
                    return
            # Unmatched response — evidence only.
            self.contamination.append({"line": line, "unmatched_response": True})
            return

        if msg.get("method") == "event" and isinstance(msg.get("params"), dict):
            event_name = msg["params"].get("event")
            if isinstance(event_name, str):
                self._dispatch_event(event_name, msg["params"].get("data"))
                return

        self.contamination.append({"line": line, "unclassified": True})

    def _dispatch_event(self, event: str, data) -> None:
        with self._subs_lock:
            self._event_counts[event] = self._event_counts.get(event, 0) + 1
            buf = self._subs.get(event)
            if buf is None:
                return
            buf.append(data)
            if len(buf) > 4096:
                del buf[: len(buf) - 4096]

    # -------------------------------------------------------------- write path

    def _next_request_id(self) -> int:
        with self._id_lock:
            self._next_id += 1
            return self._next_id

    def call(self, method: str, params: list | None = None, timeout: float = DEFAULT_CALL_TIMEOUT):
        """Send one request and block for its id-matched response.

        Raises RpcError on an error response, RpcTimeout if the daemon does not
        answer in time, NodeExited if the process died meanwhile.
        """
        if params is None:
            params = []
        rid = self._next_request_id()
        q = queue.Queue(maxsize=1)
        with self._pending_lock:
            if self._dead:
                raise NodeExited(self._dead_code)
            self._pending[rid] = q
        line = json.dumps(
            {"jsonrpc": "2.0", "id": rid, "method": method, "params": params},
            separators=(",", ":"),
        ) + "\n"
        try:
            with self._write_lock:
                if self._dead:
                    raise NodeExited(self._dead_code)
                _ensure_pipe(self.proc.stdin).write(line.encode("utf-8"))
                _ensure_pipe(self.proc.stdin).flush()
        except (BrokenPipeError, OSError) as exc:
            with self._pending_lock:
                self._pending.pop(rid, None)
            raise NodeExited(self._dead_code) from exc

        try:
            resp = q.get(timeout=timeout)
        except queue.Empty:
            with self._pending_lock:
                self._pending.pop(rid, None)
            raise RpcTimeout(f"{method} timed out after {timeout}s")
        if "__exited__" in resp:
            raise NodeExited(resp["__exited__"])
        if "error" in resp:
            err = resp["error"]
            raise RpcError(err.get("code"), err.get("message", "unknown error"))
        return resp.get("result")

    # ------------------------------------------------------------- event API

    def subscribe(self, event: str) -> list:
        """Return the shared buffer for `event` (created on demand).

        All subscribers of the same event share one buffer; wait_event and
        drain_events operate on it under the subscription lock, and
        event_count() gives a monotonic dispatch total for exact-count
        assertions.
        """
        with self._subs_lock:
            return self._subs.setdefault(event, [])

    def event_count(self, event: str) -> int:
        with self._subs_lock:
            return self._event_counts.get(event, 0)

    def _buf_snapshot_matching(self, buf: list, predicate) -> list:
        """Under the lock, extract matching items from the shared buffer."""
        with self._subs_lock:
            keep = []
            out = []
            for item in buf:
                if predicate is None or predicate(item):
                    out.append(item)
                else:
                    keep.append(item)
            buf[:] = keep
        return out

    def wait_event(self, event: str, timeout: float, predicate=None, poll: float = 0.05):
        """Block until an event matching `predicate` arrives or timeout.

        Returns the event `data`. Raises RpcTimeout on deadline.
        """
        buf = self.subscribe(event)
        deadline = time.monotonic() + timeout
        while True:
            found = self._buf_snapshot_matching(buf, predicate)
            if found:
                return found[0]
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise RpcTimeout(f"event '{event}' not observed within {timeout}s")
            time.sleep(min(poll, remaining))

    def drain_events(self, event: str, predicate=None) -> list:
        """Consume and return all currently buffered events for `event`."""
        return self._buf_snapshot_matching(self.subscribe(event), predicate)

    # ---------------------------------------------------------------- control

    def close_stdin(self) -> None:
        """Send EOF by closing the stdin write end (graceful shutdown path)."""
        try:
            _ensure_pipe(self.proc.stdin).close()
        except (BrokenPipeError, OSError):
            pass

    def wait_exit(self, timeout: float) -> tuple[int | None, bool]:
        """Wait for process exit. Returns (exit_code, timed_out)."""
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            code = self.proc.poll()
            if code is not None:
                self.exit_code = code
                return code, False
            time.sleep(0.05)
        return None, True

    def kill(self) -> None:
        """Force-kill. Windows: taskkill /T /F (process tree). Unix: SIGTERM→SIGKILL."""
        self.kill_required = True
        if sys.platform == "win32":
            subprocess.run(
                ["taskkill", "/PID", str(self.proc.pid), "/T", "/F"],
                capture_output=True,
                timeout=20,
            )
        else:
            try:
                self.proc.terminate()
                self.proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait(timeout=5)


# ---------------------------------------------------------------- helpers

def _ensure_pipe(stream):
    """Popen pipes are guaranteed non-None here (spawned with stdin/stdout/
    stderr=PIPE); narrow the type for the static checker."""
    if stream is None:
        raise RuntimeError("unexpectedly missing stdio pipe")
    return stream


def port_in_use(port: int, timeout: float = 1.0) -> bool:
    """True if a TCP listener answers on 127.0.0.1:port (any bind)."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.settimeout(timeout)
        try:
            sock.connect(("127.0.0.1", port))
            return True
        except OSError:
            return False


def read_identify(host: str, port: int, timeout: float = 3.0) -> dict | None:
    """Probe the plaintext identify service (port+1). Returns the JSON dict,
    or None if the service is not answering or the payload is unparsable."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.settimeout(timeout)
        try:
            sock.connect((host, port))
        except OSError:
            return None
        chunks = []
        try:
            while True:
                data = sock.recv(4096)
                if not data:
                    break
                chunks.append(data)
        except socket.timeout:
            return None
    try:
        payload = json.loads(b"".join(chunks))
    except (json.JSONDecodeError, UnicodeDecodeError):
        return None
    if not isinstance(payload, dict) or not payload.get("peer_id") or not payload.get("uuid"):
        return None
    return payload
