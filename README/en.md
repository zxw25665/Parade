# Parade (游行)

Decentralized, end-to-end encrypted LAN collaboration toolkit. File sharing, team chat, and private messaging — no server, no cloud, no internet required.

## Quick Start

```bash
go build -o parade ./cmd/parade/
./parade daemon --debug
```

Requires Go 1.26+. No CGO, no external dependencies.

## Features

- **Team Chat** — Encrypted group messaging via libp2p GossipSub
- **Private Chat** — ECDH-encrypted one-to-one messaging
- **File Sharing** — Shared virtual directories, 2MB chunked transfers with resume
- **Offline-First** — Full local operation; syncs automatically when peers reconnect
- **No Server** — Pure P2P over LAN; no registration, no cloud, no infrastructure

## Architecture

```
cmd/parade/           CLI entrypoint (daemon, version)
├── main.go
└── daemon/
    ├── daemon.go         Engine wiring, mode control, signal handling
    └── lockfile.go       Single-instance flock

internal/
├── app/               Business orchestration, JSON-RPC API (32 methods)
│   ├── app.go             Register, Login, JoinTeam, SendTeamChat, etc.
│   ├── interfaces.go      NetworkEngine, FileEngine, Frontend contracts
│   ├── hlc.go             Hybrid Logical Clock generator
│   ├── derived_id.go      Deterministic UUID derivation
│   ├── jsonrpc.go         Method registry (reflection-based dispatch)
│   ├── uds_ui.go          UDS broadcast to frontend clients
│   └── uds_listener.go    UDS accept loop + JSON-RPC dispatcher
├── core/
│   ├── sync/              Sparse Time Bucket Merkle Tree sync algorithm
│   │   ├── timebucket.go      HLC → bucket path derivation
│   │   ├── merkle.go          Merkle tree construction (BLAKE3)
│   │   ├── freeze.go          Daily bucket freezing, 14-day window
│   │   ├── sync.go            Level-by-level comparison, bidirectional exchange
│   │   └── testdata.go        Deterministic dataset generators
│   ├── eventbus/          In-memory async pub/sub
│   ├── crypto/            Identity + AES-256-GCM / Curve25519 / Argon2
│   └── db/                SQLite (WAL mode, modernc.org/sqlite, no CGO)
├── network/            libp2p P2P layer
│   ├── libp2p_engine.go     Host setup, peer management
│   ├── libp2p_chat.go       GossipSub + private chat streams
│   ├── libp2p_connect.go    3-phase connection handshake
│   ├── libp2p_file.go       File metadata/download/browse
│   ├── libp2p_sync.go       Legacy linear HLC sync (fallback)
│   ├── libp2p_merklesync.go Merkle sync protocol handler
│   └── libp2p_host.go       libp2p host setup
└── file/               Virtual file tree, 2MB chunk I/O, BLAKE3 hashing
    ├── vfs.go, chunk.go, hash.go, chunk_tracker.go, transfer.go
```

### Data Flow

```
Frontend (TBD) ←UDS/JSON-RPC→ parade daemon ←EventBus→ Network / File / Crypto / DB / Sync
```

## CLI

```bash
parade daemon [flags]

  --headless     No UDS listener (automation/CI)
  --debug        Multi-instance allowed, custom P2P interface
  --production   Force loopback P2P, single-instance lock
  --data-dir     Data directory (default: ./.parade_data)
  --uds          UDS socket path (default: /tmp/parade.sock)
  --port         P2P listen port (default: 4327)
  --listen       P2P listen address (default: 127.0.0.1)
```

## IPC Protocol

JSON-RPC 2.0 over Unix domain socket, newline-delimited.

```json
{"jsonrpc":"2.0","id":1,"method":"SendTeamChat","params":["hello"]}
{"jsonrpc":"2.0","id":1,"result":null}
{"jsonrpc":"2.0","method":"event","params":{"event":"ui_new_message","data":{...}}}
```

RPC client: `tests/rpc_client.py` (Python 3, no deps).

## Sync Algorithm

Parade uses a **Sparse Time Bucket Merkle Tree** for conversation synchronization:

```
Level 0: Year  (YYYY)
Level 1: Month (YYYY-MM)
Level 2: Day   (YYYY-MM-DD)
Level 3: Hour  (YYYY-MM-DDTHH)
Level 4: Message (individual HLC)
```

- Only buckets containing messages are created (sparse)
- Each level forms a Merkle tree with BLAKE3 hashes
- Sync compares roots → drills down on mismatch → bidirectional exchange at hour level
- Daily 00:00 UTC freeze prevents re-comparison of past buckets
- 14-day window for pruning frozen buckets
- Automatic fallback to legacy linear HLC sync on error

Protocol: `/parade/merklesync/1.0.0` (6 message types, 30s timeout)

## Test Suite

```bash
./tests/test_all.sh          # 30 tests across 6 phases
```

| Phase | What | Count |
|-------|------|-------|
| Build | Compile binary | 1 |
| Unit | `go test ./...` (7 packages) | 7 |
| Benchmarks | 9 sync benchmarks | 9 |
| Correctness | 3-node/5-node sync, edge cases | 18 |
| Cluster | 5-node integration (partition tolerance) | ~80 steps |
| Architecture | File existence, models, vet | 12 |

### Key Correctness Tests

| Test | What It Verifies |
|------|------------------|
| `Test3Node_DatasetA_FullSync` | 3 nodes, 500 msgs, 1 conv, full mesh |
| `Test3Node_DatasetB_FullSync` | 3 nodes, 500 msgs, 2 convs |
| `Test3Node_PartialSync` | 100%/60%/30% subsets converge |
| `Test3Node_IdempotentSync` | Second sync = zero transfer |
| `Test5Node_ChainedSync` | Chain 0→1→2→3→4 converges |
| `Test5Node_StarSync` | Star 0→1,2,3,4 converges |
| `Test5Node_GradualConvergence` | 20%-60% subsets, full mesh |
| `TestEmptySync` | Empty conversation sync |
| `TestSyncWithFrozenBuckets` | Frozen buckets trusted |
| `TestSync_ContentTamperingDetection` | Tampered content → different root hash |
| `TestConcurrentSync_Safety` | Goroutine-safe concurrent sync |
| `TestLargeDataset_TreeSize` | 10K msgs → 77 tree nodes |

### Benchmarks

| Benchmark | Time | Memory |
|-----------|------|--------|
| Build tree (500 msgs) | ~500 µs | 266 KB |
| 3-node full sync | ~2.0 ms | 1.4 MB |
| 5-node full sync | ~3.9 ms | 2.8 MB |
| Compute message hash | ~320 ns | 112 B |
| Bucket hash (100 children) | ~2.6 µs | 128 B |

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Language | Go 1.26 | Single binary, cross-compile, great concurrency |
| Database | modernc.org/sqlite | Pure Go, no CGO, WAL mode |
| P2P | libp2p | Battle-tested, protocol streams, NAT traversal |
| Crypto | AES-256-GCM, Curve25519, Argon2id, BLAKE3 | Standard, audited, no surprises |
| IPC | JSON-RPC 2.0 over UDS | Simple, debuggable, language-agnostic |
| Frontend | TBD (Tauri/Vue3 planned) | Old Wails frontend in `deprecated/frontend/` |

## Key Conventions

- **Interface-driven**: All engines defined as interfaces in `internal/app/interfaces.go`
- **Fluent builders**: `file.NewEngine().WithDatabase(d).WithEventBus(b)`
- **HLC ordering**: `2006-01-02T15:04:05.000Z_0001_NodeID` — lexicographically sortable
- **Idempotent inserts**: `ON CONFLICT(hlc) DO NOTHING` — safe to retransmit
- **BLAKE3**: Used for both file hashing and Merkle tree hashing
- **2MB chunks**: Fixed chunk size with `sync.Pool` reuse

## Development

```bash
go build ./...                       # Build all packages
go test ./...                        # All unit tests
go test -v -count=1 ./internal/core/sync/...  # Sync tests
go test -bench=. ./internal/core/sync/...     # Benchmarks
./tests/test_all.sh                  # Full test suite
```

No Makefile. Standard Go toolchain only.

## Known Issues

- mDNS peer discovery is not functional; peers connect via explicit IP
- Old Wails frontend in `deprecated/frontend/` — replacement TBD
- gRPC protobuf stubs in `proto/` are placeholders for future migration
