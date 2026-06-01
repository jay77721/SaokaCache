# SaokaCache

Distributed in-memory caching system written in Go, featuring gRPC service layer, etcd-based service discovery, and anti-cache-problem mechanisms (bloom filter, singleflight, TTL jitter).

## Quick Commands

```bash
# Build all packages
go build ./...

# Run tests
go test ./...

# Run tests with verbose output
go test -v ./...

# Run specific package tests
go test ./store/...

# Run benchmarks
go test -bench=. ./store/...

# Run tests with race detector
go test -race ./...

# Generate protobuf code (requires protoc + plugins)
protoc --go_out=. --go-grpc_out=. pb/kama.proto

# Run example (multi-node)
go run example/test.go -port 8001 -node node1
```

## Project Structure

```
.
├── group.go              # Core: named cache namespace with Get/Set/Delete
├── cache.go              # Cache layer: bloom filter + singleflight + store
├── byteview.go           # Immutable byte slice wrapper (all methods return copies)
├── client.go             # gRPC client implementing Peer interface
├── server.go             # gRPC server wrapping cache groups
├── peers.go              # PeerPicker + ClientPicker with etcd discovery
├── utils.go              # Peer address validation
├── bloom/                # Bloom filter (anti-penetration)
├── consistenthash/       # Consistent hashing with virtual nodes + auto-rebalancing
├── singleflight/         # Request deduplication (anti-breakdown)
├── store/                # Store interface + LRU/LRU2 implementations
│   ├── store.go          # Store factory (LRU, LRU2)
│   ├── lru.go            # Standard LRU using container/list
│   ├── lru2.go           # Two-level LRU with segmented locks + custom linked list
│   └── lru2_test.go      # Unit + benchmark tests
├── registry/             # etcd service registration (lease + keepalive)
├── pb/                   # Protobuf definitions + generated code
└── example/              # Multi-node demo (entry point)
```

## Architecture

- **Group** is the primary user-facing API — a named cache namespace backed by a `Getter` callback for cache-miss loading.
- **Cache** wraps a `Store` with bloom filter (anti-penetration) and singleflight (anti-breakdown). TTL jitter on `AddWithExpiration` prevents cache avalanche.
- **Store** interface (`store/store.go`) supports LRU and LRU2. LRU2 uses segmented buckets with per-bucket mutexes and a two-level hot/warm cache.
- **PeerPicker** uses consistent hashing to distribute keys across nodes; **ClientPicker** discovers peers via etcd watch.
- **Server/Client** communicate via gRPC (protobuf-defined in `pb/kama.proto`).

## Code Conventions

- **Language**: Comments, error messages, and log output are in **Chinese (中文)**.
- **Pattern**: Functional options (`type XxxOption func(*Xxx)`) for all configurable types.
- **Immutability**: `ByteView` is immutable — all public methods return copies, never mutate.
- **Concurrency**: `atomic` for lock-free fast paths; `sync.Mutex` for store operations; singleflight for request dedup.
- **Errors**: `var ErrXxx = errors.New(...)` at package level.
- **Logging**: `logrus` with `[SaokaCache]` prefix.
- **Testing**: Standard `testing` package with sub-tests (`t.Run`); no external test frameworks. Tests and comments in Chinese.

## Key Dependencies

| Dependency | Purpose |
|---|---|
| `google.golang.org/grpc` | gRPC server/client |
| `google.golang.org/protobuf` | Protobuf serialization |
| `go.etcd.io/etcd/client/v3` | etcd service discovery |
| `github.com/sirupsen/logrus` | Structured logging |

## Adding a New Store Implementation

1. Create `store/xxx.go` implementing the `Store` interface in `store/store.go`.
2. Register it in `NewStore()` factory function.
3. Add tests in `store/xxx_test.go`.
4. LRU2 (`store/lru2.go`) is the reference for production-quality implementation — follow its patterns for segmented locking and benchmark coverage.

## Adding a New RPC

1. Define the method in `pb/kama.proto`.
2. Regenerate protobuf: `protoc --go_out=. --go-grpc_out=. pb/kama.proto`.
3. Implement the server-side handler in `server.go`.
4. Implement the client-side call in `client.go`.
5. Wire into the `Group` or `Cache` layer as needed.

## Gotchas

- LRU2 bucket capacity is limited to `uint16` max (65535 items per bucket) due to array-based linked list indices.
- The custom clock in `lru2.go` updates every 100ms — `time.Now()` calls are approximated for performance.
- Consistent hash rebalancing runs every 1s when imbalance > 25% — expect peer reassignment under load.
- etcd lease TTL is 10s with keepalive — nodes appear/disappear from the cluster with this granularity.
- No CI/CD, Makefile, or Dockerfile — build and test via `go` commands only.
