"""Parade multi-node process-level E2E scenarios (T0-T9).

Drives REAL `parade daemon` processes over stdio JSON-RPC (no --headless,
ordinary --debug IPC mode), with isolated data dirs, explicit port pairs
(P = BASE+2i for p2p, P+1 = identify), unique temp run roots, bounded
evidence, and EOF-first cleanup with taskkill /T /F fallback on Windows.

Scenario status per docs/多节点实际测试方案.md:
  T0-T5  deterministic single-node lifecycle / persistence / events.
  T6     cross-port connection gate; doubles as the seam probe. When the
         production seam (ConnectToPeer honoring ip:port) is present, the gate
         inverts and T7/T8 auto-enable; otherwise they SKIP with the exact
         blocking reason and never silently pass.
  T7/T8  two-node chat convergence / offline catch-up (seam-gated).
  T9     mDNS diagnostics only — never part of pass/fail.

Gate: run only when PARADE_E2E=1 (run.ps1 / run.sh enforce this).
"""

from __future__ import annotations

import argparse
import concurrent.futures
import json
import os
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import traceback
from datetime import datetime, timezone
from pathlib import Path

from rpc_stdio import (
    KNOWN_PRE_STARTUP_STDOUT,
    Node,
    NodeExited,
    RpcError,
    RpcTimeout,
    port_in_use,
    read_identify,
)

REPO_ROOT = Path(__file__).resolve().parent.parent
RUN_ID = datetime.now().strftime("%Y%m%d-%H%M%S") + "-" + os.urandom(4).hex()
TEAM_SECRET = "parade-multinode-e2e-secret"
EVENT_NEW_MESSAGE = "ui_new_message"
EVENT_CONV_UPDATED = "ui_conversation_updated"
EVENT_PEER_JOINED = "ui_peer_joined"

# Expected per-node files in the data dir after identity + team setup
# (libp2p_engine.savePeers and config.toml only appear after save paths are
# exercised — those are asserted in T4, not here).
DATA_DIR_FILES_AFTER_JOIN = (".parade_data.db", ".parade_identity", ".parade_teams", ".parade.log")


class Ctx:
    def __init__(self, binary: str, workdir: Path, base_dir: Path, base_port: int,
                 keep_artifacts: bool, no_mdns_round: bool, n_nodes: int):
        self.binary = binary
        self.workdir = workdir
        self.base_dir = base_dir
        self.artifacts = base_dir / "artifacts"
        self.artifacts.mkdir(parents=True, exist_ok=True)
        self.base_port = base_port
        self.keep_artifacts = keep_artifacts
        self.no_mdns_round = no_mdns_round
        self.n_nodes = n_nodes
        self.nodes: dict[str, Node] = {}
        self.results: list[dict] = []
        self.team_id: str | None = None
        self.node0_pubkey: str | None = None
        self.node1_pubkey: str | None = None
        self.seam: dict = {"detected": None, "probe": None, "skip_reason": None}
        self.mdns: dict = {}
        self.cwd_baseline: set[str] = set()
        self.env_overrides: dict[str, str] = {}

    def record(self, case_id: str, name: str, status: str, detail: str = "", duration_s: float = 0.0):
        self.results.append({
            "id": case_id, "name": name, "status": status,
            "detail": detail, "duration_s": round(duration_s, 2),
        })
        print(f"[{status:4}] {case_id} {name} ({duration_s:.1f}s)" + (f" — {detail}" if detail else ""))

    def failed(self) -> bool:
        return any(r["status"] == "FAIL" for r in self.results)


# ------------------------------------------------------------- port helpers

def candidate_ports(base_port: int, n_nodes: int) -> list[int]:
    ports = []
    for i in range(n_nodes):
        ports.append(base_port + 2 * i)      # p2p
        ports.append(base_port + 2 * i + 1)  # identify (p2p+1)
    return ports


def occupier_hint(port: int) -> str:
    """Best-effort owner of a busy port (Windows netstat; else nothing)."""
    try:
        if os.name == "nt":
            out = subprocess.run(
                ["netstat", "-ano"], capture_output=True, text=True, timeout=10
            ).stdout
            for line in out.splitlines():
                if f":{port} " in line and "LISTENING" in line:
                    pid = line.rsplit(" ", 1)[-1].strip()
                    return f"listening PID {pid}"
        return ""
    except (OSError, subprocess.SubprocessError):
        return ""


# ------------------------------------------------------------- node helpers

def spawn_node(ctx: Ctx, name: str, p2p_port: int, mdns: bool = True,
               data_dir: Path | None = None) -> Node:
    data_dir = data_dir or (ctx.base_dir / f"{name}-data")
    node = Node(name, ctx.binary, data_dir, p2p_port, ctx.workdir, ctx.artifacts, mdns=mdns)
    ctx.nodes[name] = node
    return node


def wait_r2(node: Node, timeout: float = 10.0) -> bool:
    """IPC ready: CheckHasIdentity round trip answers within the deadline."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            node.call("CheckHasIdentity", timeout=2.0)
            return True
        except (RpcTimeout, NodeExited):
            if node.exit_code is not None:
                return False
            time.sleep(0.2)
    return False


def wait_r3(node: Node, timeout: float = 30.0) -> dict:
    """Network ready: identify service on p2p+1 answers with peer_id/uuid,
    with .parade.log engine/identify lines as corroborating evidence."""
    deadline = time.monotonic() + timeout
    log_lines = []
    payload = None
    while time.monotonic() < deadline:
        payload = read_identify("127.0.0.1", node.identify_port)
        if payload is not None:
            break
        log = node.data_dir / ".parade.log"
        if log.exists():
            try:
                log_lines = log.read_text(encoding="utf-8", errors="replace").splitlines()
            except OSError:
                pass
        if any(f"libp2p engine started on port {node.p2p_port}" in ln for ln in log_lines) and \
           any(f"identify server on :{node.identify_port}" in ln for ln in log_lines):
            break
        time.sleep(0.5)
    return {
        "identify": payload,
        "log_engine_started": any(f"libp2p engine started on port {node.p2p_port}" in ln for ln in log_lines),
        "log_identify_server": any(f"identify server on :{node.identify_port}" in ln for ln in log_lines),
    }


def ensure_identity(node: Node, password: str) -> bool:
    """Register only when no identity exists yet; returns whether registered."""
    has = node.call("CheckHasIdentity")
    if not has:
        node.call("Register", [password])
        assert node.call("CheckHasIdentity") is True, "identity file not created by Register"
        return True
    return False


def login_and_join(ctx: Ctx, node: Node, password: str) -> None:
    node.call("Login", [password])
    node.call("JoinTeam", [TEAM_SECRET])
    team = node.call("GetActiveTeam")
    assert isinstance(team, str) and team, "GetActiveTeam empty after JoinTeam"
    r3 = wait_r3(node)
    assert r3["identify"] is not None, "identify service (port+1) never answered"
    assert r3["log_engine_started"], ".parade.log missing 'libp2p engine started'"
    assert r3["log_identify_server"], ".parade.log missing 'identify server on'"
    if ctx.team_id is None:
        ctx.team_id = team
    else:
        assert team == ctx.team_id, f"team id mismatch across nodes: {team} != {ctx.team_id}"


def team_conversation_id(node: Node) -> str:
    convs = node.call("ListConversations")
    for conv in convs:
        if conv.get("type") == "team":
            return conv["id"]
    raise AssertionError("no team conversation in ListConversations")


def conversation_messages(node: Node, conv_id: str) -> list[dict]:
    return node.call("GetConversationMessages", [conv_id, 1000, 0])


def message_contents(node: Node, conv_id: str) -> list[str]:
    return [m["content"] for m in conversation_messages(node, conv_id)]


def close_node(ctx: Ctx, node: Node, timeout: float = 10.0) -> None:
    """EOF-first graceful shutdown; taskkill /T /F fallback. Records FAIL
    evidence on non-zero exit or timeout."""
    node.close_stdin()
    code, timed_out = node.wait_exit(timeout)
    if timed_out:
        node.kill()
        node.wait_exit(5)
        ctx.record("CLEAN", f"{node.name} shutdown", "FAIL",
                   f"stdin EOF did not exit in {timeout}s — taskkill fallback used")
    elif code != 0:
        ctx.record("CLEAN", f"{node.name} shutdown", "FAIL",
                   f"exit code {code} after stdin EOF (expected 0)")


def dump_state(ctx: Ctx, label: str) -> None:
    """On failure, dump live node state to evidence files before teardown."""
    for name, node in ctx.nodes.items():
        if not node.alive():
            continue
        state = {}
        for method, params in (
            ("GetActiveTeam", None),
            ("ListSavedPeers", None),
            ("ListConversations", None),
            ("GetPeersWithStatus", None),
        ):
            try:
                state[method] = node.call(method, params or [], timeout=3)
            except Exception as exc:  # evidence best-effort
                state[method] = f"<unavailable: {exc}>"
        try:
            conv = team_conversation_id(node)
            state["GetConversationMessages"] = conversation_messages(node, conv)
        except Exception as exc:
            state["GetConversationMessages"] = f"<unavailable: {exc}>"
        path = ctx.artifacts / f"evidence-state-{label}-{name}.json"
        path.write_text(json.dumps(state, indent=2, default=str), encoding="utf-8")


def cleanup(ctx: Ctx) -> None:
    """Mandatory teardown on every path: EOF → wait → taskkill; then port
    release and residue re-check. Results feed the report."""
    for name, node in reversed(list(ctx.nodes.items())):
        if node.alive():
            close_node(ctx, node)
    # Ports must be released; our PIDs must be gone.
    busy = [p for p in candidate_ports(ctx.base_port, ctx.n_nodes) if port_in_use(p)]
    if busy:
        ctx.record("CLEAN", "port release", "FAIL",
                   "ports still bound after teardown: " + ", ".join(str(p) for p in busy))
    residue = [name for name, node in ctx.nodes.items() if node.alive()]
    if residue:
        ctx.record("CLEAN", "process residue", "FAIL",
                   "nodes still alive after teardown: " + ", ".join(residue))
    for name, node in ctx.nodes.items():
        log = node.data_dir / ".parade.log"
        if log.exists():
            try:
                shutil.copy2(log, ctx.artifacts / f"{name}-data-parade.log")
            except OSError:
                pass
        if node.contamination:
            (ctx.artifacts / f"{name}-stdout-contamination.txt").write_text(
                "\n".join(json.dumps(c, ensure_ascii=False) for c in node.contamination),
                encoding="utf-8",
            )


# ------------------------------------------------------------------- T0

def t0_preflight(ctx: Ctx) -> None:
    start = time.monotonic()
    problems = []
    warnings = []

    if not os.path.isfile(ctx.binary):
        problems.append(f"binary not found: {ctx.binary} (run `pixi run build` first)")

    busy = [p for p in candidate_ports(ctx.base_port, ctx.n_nodes) if port_in_use(p)]
    if busy:
        hints = " | ".join(f"{p} ({occupier_hint(p)})" for p in busy)
        problems.append(f"candidate ports already in use: {hints}")

    global_cfg = []
    if os.name == "nt":
        appdata = os.environ.get("APPDATA")
        if appdata:
            global_cfg.append(Path(appdata) / "parade" / "config.toml")
    else:
        xdg = os.environ.get("XDG_CONFIG_HOME")
        global_cfg.append(Path(xdg) / "parade" / "config.toml" if xdg else Path.home() / ".config" / "parade" / "config.toml")
    existing = [str(p) for p in global_cfg if p.exists()]
    if existing:
        warnings.append(
            "global config would apply to EVERY node: " + "; ".join(existing)
            + " (run in an isolated environment to silence)"
        )

    ctx.env_overrides = {k: v for k, v in os.environ.items() if k.startswith("PARADE_")}
    if ctx.env_overrides:
        warnings.append("inherited PARADE_* env vars (stripped per node): "
                        + ", ".join(sorted(ctx.env_overrides)))

    ctx.cwd_baseline = {p.name for p in Path(ctx.workdir).glob(".parade*")}

    if problems:
        ctx.record("T0", "preflight", "FAIL", "; ".join(problems), time.monotonic() - start)
    elif warnings:
        ctx.record("T0", "preflight", "WARN", "; ".join(warnings), time.monotonic() - start)
    else:
        ctx.record("T0", "preflight", "PASS", "", time.monotonic() - start)


# ------------------------------------------------------------------- T1

def t1_lifecycle(ctx: Ctx, node: Node) -> None:
    start = time.monotonic()
    node_pw = f"pass-{node.name}-{RUN_ID}"

    time.sleep(1.0)
    assert node.alive(), "node exited within 1s of spawn (R1 failed)"

    assert wait_r2(node), "CheckHasIdentity round trip failed within 10s (R2 failed)"
    has = node.call("CheckHasIdentity")
    assert has is False, f"fresh data dir reports identity: {has!r}"

    close_node(ctx, node)
    assert node.exit_code == 0, f"EOF exit code {node.exit_code} (expected 0)"
    ctx.record("T1", "single-daemon stdio lifecycle", "PASS", "", time.monotonic() - start)


# ------------------------------------------------------------------- T2

def t2_identity_network(ctx: Ctx, node: Node) -> None:
    start = time.monotonic()
    node_pw = f"pass-{node.name}-{RUN_ID}"

    assert wait_r2(node), "CheckHasIdentity round trip failed (R2)"
    registered = ensure_identity(node, node_pw)
    assert registered, "T2 expects a fresh identity (Register)"
    node.call("Login", [node_pw])
    node.call("JoinTeam", [TEAM_SECRET])

    team = node.call("GetActiveTeam")
    assert isinstance(team, str) and team, "GetActiveTeam empty"
    pubkey = node.call("GetPubKey")
    assert isinstance(pubkey, str) and pubkey, "GetPubKey empty"
    if ctx.node0_pubkey is None:
        ctx.node0_pubkey = pubkey

    r3 = wait_r3(node)
    assert r3["identify"] is not None, "identify service (port+1) never answered"
    assert r3["log_engine_started"] and r3["log_identify_server"], \
        f".parade.log lacks engine/identify lines: {r3}"

    missing = [f for f in DATA_DIR_FILES_AFTER_JOIN if not (node.data_dir / f).exists()]
    assert not missing, "data dir missing files after join: " + ", ".join(missing)

    now_cwd = {p.name for p in Path(ctx.workdir).glob(".parade*")}
    new_cwd = now_cwd - ctx.cwd_baseline
    assert not new_cwd, "daemon wrote .parade_* into the working directory: " + ", ".join(sorted(new_cwd))

    ctx.record("T2", "identity/team/network + isolation", "PASS",
               f"team={team[:8]}… identify={r3['identify']['peer_id'][:8]}…",
               time.monotonic() - start)


# ------------------------------------------------------------------- T3

def t3_multiplex_events(ctx: Ctx, node: Node) -> None:
    start = time.monotonic()
    content = f"ping-{RUN_ID}-t3"
    node.subscribe(EVENT_NEW_MESSAGE)

    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        futs = {
            pool.submit(node.call, m, [], 10.0): m
            for m in ("CheckHasIdentity", "GetPubKey", "GetActiveTeam")
        }
        got = {}
        for fut in concurrent.futures.as_completed(futs):
            got[futs[fut]] = fut.result()
    assert got["CheckHasIdentity"] is True
    assert isinstance(got["GetPubKey"], str) and got["GetPubKey"]
    assert isinstance(got["GetActiveTeam"], str) and got["GetActiveTeam"]

    node.call("SendTeamChat", [content])
    ev = node.wait_event(EVENT_NEW_MESSAGE, 10.0,
                         predicate=lambda d: d.get("content") == content)
    assert ev.get("conversation_id"), "ui_new_message missing conversation_id"

    conv = team_conversation_id(node)
    assert conv == ev["conversation_id"], "event/ListConversations conv id mismatch"
    assert content in message_contents(node, conv), "message missing from GetConversationMessages"

    ctx.record("T3", "stdio multiplexing + events", "PASS", "", time.monotonic() - start)


# ------------------------------------------------------------------- T4

def t4_restart_persistence(ctx: Ctx, node: Node) -> None:
    start = time.monotonic()
    node_pw = f"pass-{node.name}-{RUN_ID}"
    saved_content = f"ping-{RUN_ID}-t3"
    conv = team_conversation_id(node)

    assert node.call("CheckHasIdentity") is True, "identity not persisted across restart"
    node.call("Login", [node_pw])
    team = node.call("GetActiveTeam")
    assert team == ctx.team_id, f"active team changed across restart: {team} != {ctx.team_id}"

    contents = message_contents(node, conv)
    assert saved_content in contents, "T3 message lost across restart"

    node.call("SavePeer", ["10.0.0.5"])
    assert node.call("ListSavedPeers") == ["10.0.0.5"], "ListSavedPeers after SavePeer"
    cfg = (node.data_dir / "config.toml")
    assert cfg.exists(), "config.toml not written by SavePeer"
    cfg_text = cfg.read_text(encoding="utf-8", errors="replace")
    assert re.search(r'saved\s*=\s*\["10\.0\.0\.5"\]', cfg_text), \
        f"[peers] saved missing from config.toml:\n{cfg_text}"

    node.call("RemovePeer", ["10.0.0.5"])
    assert node.call("ListSavedPeers") == [], "ListSavedPeers after RemovePeer"

    ctx.record("T4", "restart persistence (identity/team/messages/saved-peers)", "PASS",
               "", time.monotonic() - start)


# ------------------------------------------------------------------- T5

def t5_event_integrity(ctx: Ctx, node: Node) -> None:
    start = time.monotonic()
    c1, c2 = f"t5a-{RUN_ID}", f"t5b-{RUN_ID}"
    node.subscribe(EVENT_NEW_MESSAGE)

    node.call("SendTeamChat", [c1])
    node.call("SendTeamChat", [c2])

    ev1 = node.wait_event(EVENT_NEW_MESSAGE, 10.0, predicate=lambda d: d.get("content") == c1)
    ev2 = node.wait_event(EVENT_NEW_MESSAGE, 10.0, predicate=lambda d: d.get("content") == c2)
    assert ev1["hlc"] < ev2["hlc"], f"event HLC order violated: {ev1['hlc']} !< {ev2['hlc']}"

    close_node(ctx, node)  # EOF flushes the pipe — then count exactly
    assert node.exit_code == 0
    total = node.event_count(EVENT_NEW_MESSAGE)
    matching = [d for d in node.drain_events(EVENT_NEW_MESSAGE)
                if d.get("content") in (c1, c2)]
    assert total == 2 and len(matching) == 2, \
        f"expected exactly 2 ui_new_message, dispatched={total} drained={len(matching)}"

    ctx.record("T5", "single-node event integrity", "PASS", "", time.monotonic() - start)


# ------------------------------------------------------------------- T6

def t6_connect_gate(ctx: Ctx, n0: Node, n1: Node) -> None:
    start = time.monotonic()
    pk0 = n0.call("GetPubKey")
    pk1 = n1.call("GetPubKey")
    assert pk0 and pk1 and pk0 != pk1, "nodes share pubkeys — isolation broken"

    # Pre-seam deterministic behavior: plain-IP ConnectToPeer dials the
    # CALLER's own ports (identify p2p+1, then p2p) — phase1 failure or
    # self-connect are the only outcomes; it can never reach the other node.
    r_self = n0.call("ConnectToPeer", ["127.0.0.1"])
    p1 = r_self.get("phase1", {})
    self_ok = (p1.get("success") is False) or (r_self.get("pubkey") == pk0)
    assert self_ok, f"unexpected plain-IP ConnectToPeer result: {json.dumps(r_self)[:400]}"
    assert r_self.get("pubkey") != pk1, "plain-IP ConnectToPeer reached the other node (unexpected)"

    # Seam probe: explicit ip:port. Before the seam this cannot parse
    # ("127.0.0.1:<port>:<port+1>") and phase1 fails; after the seam it
    # connects deterministically and returns the TARGET node's pubkey.
    probe = n0.call("ConnectToPeer", [f"127.0.0.1:{n1.p2p_port}"])
    probe_p1 = probe.get("phase1", {})
    ctx.seam["probe"] = {
        "target": f"127.0.0.1:{n1.p2p_port}",
        "phase1_success": probe_p1.get("success"),
        "phase1_error": probe_p1.get("error"),
        "pubkey_is_node1": probe.get("pubkey") == pk1,
        "pubkey_is_node0": probe.get("pubkey") == pk0,
    }

    if probe_p1.get("success") is True and probe.get("pubkey") == pk1:
        ctx.seam["detected"] = True
        ctx.record("T6", "cross-port connection gate", "PASS",
                   "SEAM DETECTED — deterministic ip:port connect works; T7/T8 enabled",
                   time.monotonic() - start)
    elif probe.get("pubkey") == pk1 and probe_p1.get("success") is not True:
        ctx.seam["detected"] = False
        ctx.seam["skip_reason"] = (
            "PARTIAL seam: identify resolved the target node but the p2p dial "
            "failed (libp2p_connect.go:51 still derives the dial port from the "
            "caller's own port). T7/T8 skipped until phase1 connects."
        )
        ctx.record("T6", "cross-port connection gate", "WARN", ctx.seam["skip_reason"],
                   time.monotonic() - start)
    else:
        ctx.seam["detected"] = False
        ctx.seam["skip_reason"] = (
            "production seam absent: ConnectToPeer derives the target endpoint "
            "from the CALLER's own ports — identifyPort := e.port+1 and dial "
            "/ip4/<ip>/tcp/<e.port> (internal/network/libp2p_connect.go:38-39,51); "
            "the identify response carries no port (libp2p_engine.go:159-186) and "
            "savedPeer persists only an IP (libp2p_engine.go:639-644), so "
            "loadAndReconnect re-derives from the caller's port "
            "(libp2p_engine.go:694-733). Deterministic same-host cross-port "
            "connection is impossible; probe "
            f"ConnectToPeer('127.0.0.1:{n1.p2p_port}') returned "
            f"phase1_success={probe_p1.get('success')!r}, pubkey_is_node1="
            f"{probe.get('pubkey') == pk1}. T7/T8 auto-enable when the T6 gate inverts."
        )
        assert probe_p1.get("success") is not True and probe.get("pubkey") != pk1, \
            f"unexpected probe outcome: {json.dumps(probe)[:400]}"
        ctx.record("T6", "cross-port connection gate", "PASS",
                   "pre-seam behavior confirmed (phase1 fail / no cross-port connect)",
                   time.monotonic() - start)


# ------------------------------------------------------------------- T7/T8

def t7_convergence(ctx: Ctx, n0: Node, n1: Node) -> None:
    start = time.monotonic()
    n0.subscribe(EVENT_NEW_MESSAGE)
    n1.subscribe(EVENT_NEW_MESSAGE)
    n0.subscribe(EVENT_CONV_UPDATED)
    n1.subscribe(EVENT_CONV_UPDATED)

    sent = [f"t7-{RUN_ID}-{i}" for i in range(5)]
    for text in sent:
        n0.call("SendTeamChat", [text])

    # Collect the 5 messages on node 1 (order-independent, HLC/content keyed).
    received: dict[str, str] = {}
    deadline = time.monotonic() + 60.0
    while len(received) < 5 and time.monotonic() < deadline:
        for d in n1.drain_events(EVENT_NEW_MESSAGE):
            if d.get("content") in sent:
                received[d["content"]] = d.get("hlc", "")
        time.sleep(0.25)
    assert len(received) == 5, f"node1 received {len(received)}/5 messages: {received}"

    # Node 1 replies 2; node 0 must see them.
    replies = [f"t7-reply-{RUN_ID}-{i}" for i in range(2)]
    for text in replies:
        n1.call("SendTeamChat", [text])
    got_replies: dict[str, str] = {}
    deadline = time.monotonic() + 60.0
    while len(got_replies) < 2 and time.monotonic() < deadline:
        for d in n0.drain_events(EVENT_NEW_MESSAGE):
            if d.get("content") in replies:
                got_replies[d["content"]] = d.get("hlc", "")
        time.sleep(0.25)
    assert len(got_replies) == 2, f"node0 received {len(got_replies)}/2 replies: {got_replies}"

    # Bidirectional store convergence (content + HLC sets equal).
    conv0 = team_conversation_id(n0)
    conv1 = team_conversation_id(n1)
    msgs0 = conversation_messages(n0, conv0)
    msgs1 = conversation_messages(n1, conv1)
    set0 = {(m["content"], m["hlc"]) for m in msgs0}
    set1 = {(m["content"], m["hlc"]) for m in msgs1}
    assert set0 == set1, f"conversation stores diverged: only0={set0 - set1} only1={set1 - set0}"

    conv_updates = n0.event_count(EVENT_CONV_UPDATED) + n1.event_count(EVENT_CONV_UPDATED)
    assert conv_updates >= 1, "no ui_conversation_updated (merkle sync completion signal)"

    ctx.record("T7", "two-node chat convergence", "PASS",
               f"5->1, 2->0, stores equal, {conv_updates} conv-updated",
               time.monotonic() - start)


def t8_offline_catchup(ctx: Ctx, n0: Node, n1: Node, n1_data_dir: Path, n1_port: int) -> None:
    start = time.monotonic()
    n0.subscribe(EVENT_NEW_MESSAGE)

    close_node(ctx, n1)  # node1 offline
    assert n1.exit_code == 0

    sent = [f"t8-{RUN_ID}-{i}" for i in range(3)]
    sent_hlc: dict[str, str] = {}
    for text in sent:
        n0.call("SendTeamChat", [text])
    deadline = time.monotonic() + 20.0
    while len(sent_hlc) < 3 and time.monotonic() < deadline:
        for d in n0.drain_events(EVENT_NEW_MESSAGE):
            if d.get("content") in sent:
                sent_hlc[d["content"]] = d.get("hlc", "")
        time.sleep(0.25)
    assert len(sent_hlc) == 3, "failed to capture node0 HLCs for T8 sends"

    # Node 1 restarts on the same data dir/port; catch-up must be observable
    # from its store (auto-reconnect or explicit sync).
    n1b = spawn_node(ctx, n1.name, n1_port, data_dir=n1_data_dir)
    assert wait_r2(n1b), "node1 restart: R2 failed"
    assert n1b.call("CheckHasIdentity") is True
    n1b.call("Login", [f"pass-{n1.name}-{RUN_ID}"])
    n1b.call("JoinTeam", [TEAM_SECRET])
    conv1 = team_conversation_id(n1b)

    contents: list[str] = []
    deadline = time.monotonic() + 60.0
    while time.monotonic() < deadline:
        contents = message_contents(n1b, conv1)
        if all(s in contents for s in sent):
            break
        time.sleep(1.0)
    assert all(s in contents for s in sent), \
        f"node1 store missing offline messages: have={contents} want={sent}"

    got = {(m["content"], m["hlc"]) for m in conversation_messages(n1b, conv1) if m["content"] in sent}
    want = set(sent_hlc.items())
    assert got == want, f"catch-up HLC mismatch: got={got} want={want}"

    ctx.record("T8", "restart offline catch-up", "PASS", "all 3 messages + HLCs caught up",
               time.monotonic() - start)


# ------------------------------------------------------------------- T9

def t9_mdns_diagnostics(ctx: Ctx, nodes: list[Node], round_name: str, timeout: float,
                        expect_discovery: bool) -> None:
    start = time.monotonic()
    for n in nodes:
        n.subscribe(EVENT_PEER_JOINED)
    events: list[dict] = []
    peers: dict[str, list] = {}
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        for n in nodes:
            for d in n.drain_events(EVENT_PEER_JOINED):
                events.append({"node": n.name, "data": d, "at_s": round(time.monotonic() - start, 2)})
            try:
                status = n.call("GetPeersWithStatus", timeout=3.0)
                if status:
                    peers.setdefault(n.name, status)
            except Exception:
                pass
        if events or peers:
            if expect_discovery:
                break
        time.sleep(1.0)
    ctx.mdns[round_name] = {
        "observation_window_s": timeout,
        "peer_joined_events": events,
        "peers_with_status": {k: v for k, v in peers.items()},
        "observed_at_s": round(time.monotonic() - start, 2),
    }
    found = bool(events or peers)
    ctx.record("T9", f"mDNS diagnostic ({round_name})", "INFO",
               ("discovery observed" if found else "no discovery observed (environment-dependent, diagnostic only)"),
               time.monotonic() - start)


# ------------------------------------------------------------------- main

def run_scenarios(ctx: Ctx) -> None:
    n_nodes = ctx.n_nodes

    # ---- T1: fresh single node lifecycle ----
    n0 = spawn_node(ctx, "node0", ctx.base_port)
    t1_lifecycle(ctx, n0)
    if ctx.failed():
        return

    # ---- T2+T3: identity/team/network, multiplexing, events ----
    n0 = spawn_node(ctx, "node0", ctx.base_port)
    t2_identity_network(ctx, n0)
    if ctx.failed():
        return
    t3_multiplex_events(ctx, n0)
    if ctx.failed():
        return
    close_node(ctx, n0)
    if ctx.failed():
        return

    # ---- T4+T5: restart persistence, event integrity ----
    n0 = spawn_node(ctx, "node0", ctx.base_port)
    t4_restart_persistence(ctx, n0)
    if ctx.failed():
        return
    t5_event_integrity(ctx, n0)
    if ctx.failed():
        return

    # ---- T6 (+T7/T8 when the seam is present) + T9 round 1 ----
    n0 = spawn_node(ctx, "node0", ctx.base_port)
    n1 = spawn_node(ctx, "node1", ctx.base_port + 2)
    for node in (n0, n1):
        pw = f"pass-{node.name}-{RUN_ID}"
        assert wait_r2(node), f"{node.name}: R2 failed"
        ensure_identity(node, pw)
        login_and_join(ctx, node, pw)
    ctx.node1_pubkey = n1.call("GetPubKey")

    t6_connect_gate(ctx, n0, n1)
    if ctx.failed():
        return

    if ctx.seam["detected"]:
        t7_convergence(ctx, n0, n1)
        if ctx.failed():
            return
        t8_offline_catchup(ctx, n0, n1, n1.data_dir, n1.p2p_port)
        if ctx.failed():
            return
    else:
        for t in ("T7", "T8"):
            ctx.record(t, "blocked multi-node cases", "SKIP", ctx.seam["skip_reason"])

    t9_mdns_diagnostics(ctx, [n0, n1], "mdns-on", 30.0, expect_discovery=True)

    close_node(ctx, n0)
    close_node(ctx, n1)
    if ctx.failed():
        return

    # ---- T9 round 2: no-mdns path, diagnostics only ----
    if not ctx.no_mdns_round:
        m0 = spawn_node(ctx, "m0", ctx.base_port + 100, mdns=False)
        m1 = spawn_node(ctx, "m1", ctx.base_port + 102, mdns=False)
        for node in (m0, m1):
            pw = f"pass-{node.name}-{RUN_ID}"
            assert wait_r2(node), f"{node.name}: R2 failed"
            ensure_identity(node, pw)
            login_and_join(ctx, node, pw)
        t9_mdns_diagnostics(ctx, [m0, m1], "no-mdns", 15.0, expect_discovery=False)
        close_node(ctx, m0)
        close_node(ctx, m1)


def write_summary(ctx: Ctx, started: float) -> None:
    nodes_report = {}
    for name, node in ctx.nodes.items():
        nodes_report[name] = {
            "pid": node.pid,
            "cmd": node.cmd,
            "p2p_port": node.p2p_port,
            "identify_port": node.identify_port,
            "data_dir": str(node.data_dir),
            "exit_code": node.exit_code,
            "kill_required": node.kill_required,
            "contamination": node.contamination,
            "graceful_eof_exit": node.exit_code == 0 and not node.kill_required,
        }
    summary = {
        "run_id": RUN_ID,
        "binary": ctx.binary,
        "workdir": str(ctx.workdir),
        "base_port": ctx.base_port,
        "started_at": datetime.now(timezone.utc).isoformat(),
        "elapsed_s": round(time.monotonic() - started, 2),
        "seam": ctx.seam,
        "results": ctx.results,
        "nodes": nodes_report,
        "mdns": ctx.mdns,
        "ports": {
            str(p): {"preflight_in_use": False, "final_in_use": port_in_use(p)}
            for p in candidate_ports(ctx.base_port, ctx.n_nodes)
        },
        "artifacts_dir": str(ctx.artifacts),
    }
    (ctx.base_dir / "run-summary.json").write_text(
        json.dumps(summary, indent=2, ensure_ascii=False), encoding="utf-8")


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="Parade multi-node process E2E (T0-T9)")
    ap.add_argument("--bin", required=True, help="path to parade/parade.exe")
    ap.add_argument("--workdir", default=str(REPO_ROOT))
    ap.add_argument("--base-dir", default=None, help="run root (default: %TEMP%/parade-multinode/<run-id>)")
    ap.add_argument("--base-port", type=int, default=int(os.environ.get("PARADE_E2E_BASE_PORT", "44100")))
    ap.add_argument("--nodes", type=int, default=2)
    ap.add_argument("--keep-artifacts", action="store_true")
    ap.add_argument("--no-mdns-round", action="store_true", help="skip T9 round 2 (no-mdns)")
    args = ap.parse_args(argv)

    started = time.monotonic()
    base_dir = Path(args.base_dir) if args.base_dir else \
        Path(tempfile.gettempdir()) / "parade-multinode" / RUN_ID
    base_dir.mkdir(parents=True, exist_ok=True)

    ctx = Ctx(args.bin, Path(args.workdir), base_dir, args.base_port,
              args.keep_artifacts, args.no_mdns_round, args.nodes)
    print(f"=== Parade multi-node E2E (run {RUN_ID}) ===")
    print(f"run root : {base_dir}")
    print(f"binary   : {args.bin}")
    print(f"ports    : {candidate_ports(ctx.base_port, ctx.n_nodes)}")

    try:
        t0_preflight(ctx)
        if not ctx.failed():
            run_scenarios(ctx)
    except Exception as exc:
        dump_state(ctx, "on-exception")
        ctx.record("FATAL", "harness exception", "FAIL", f"{type(exc).__name__}: {exc}\n{traceback.format_exc()}")
    finally:
        cleanup(ctx)
        write_summary(ctx, started)

    # Post-seam re-check: once the seam exists, ANY non-JSON stdout line is a
    # protocol violation (design: tolerated only before the seam lands).
    if ctx.seam.get("detected") is True:
        for name, node in ctx.nodes.items():
            bad = [c for c in node.contamination
                   if not c.get("known_pollution") and not c.get("unmatched_response")]
            if bad:
                ctx.record("IO", f"{name} stdout purity", "FAIL",
                           f"non-JSON-RPC lines on stdout after seam: {json.dumps(bad[:5], ensure_ascii=False)}")

    print()
    for r in ctx.results:
        status = r["status"]
        mark = "OK " if status in ("PASS", "INFO") else ("!! " if status == "FAIL" else "-- ")
        print(f"{mark}[{status:4}] {r['id']} {r['name']}")
        if r["detail"] and status in ("FAIL", "SKIP", "WARN"):
            print(f"      {r['detail']}")
    n_fail = sum(1 for r in ctx.results if r["status"] == "FAIL")
    n_skip = sum(1 for r in ctx.results if r["status"] == "SKIP")
    n_pass = sum(1 for r in ctx.results if r["status"] in ("PASS", "INFO"))
    print()
    print(f"summary: {n_pass} pass, {n_fail} fail, {n_skip} skip "
          f"(elapsed {time.monotonic() - started:.1f}s)")
    print(f"artifacts: {ctx.artifacts}")
    if not ctx.keep_artifacts:
        shutil.rmtree(base_dir, ignore_errors=True)
        print(f"run root removed (pass --keep-artifacts to retain): {base_dir}")
    return 1 if n_fail else 0


if __name__ == "__main__":
    sys.exit(main())
