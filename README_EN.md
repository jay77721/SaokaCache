# SaokaCache

A distributed in-memory cache system written in Go, with gRPC communication, etcd service discovery, and triple-layer protection against cache penetration, stampede, and avalanche.

> [中文文档](README.md)

---

## Architecture

```
         ┌──────────────────────────────┐
         │        Group (User API)       │
         │   Get / Set / Delete / Stats  │
         └──────────────┬───────────────┘
                        │
         ┌──────────────▼───────────────┐
         │     CachePolicy (Policy)      │
         │  ┌─────────┬────────┬──────┐ │
         │  │ Bloom   │ Single │ TTL  │ │
         │  │ Filter  │ Flight │ Jitter│ │
         │  │(Penetra)│(Stampe)│(Avala)│ │
         │  └─────────┴────────┴──────┘ │
         └──────────────┬───────────────┘
                        │
         ┌──────────────▼───────────────┐
         │      Cache (Storage)         │
         │    lazy init / Get / Set     │
         └──────────────┬───────────────┘
                        │
         ┌──────────────▼───────────────┐
         │       Store Interface        │
         │  ┌────────┐ ┌─────────────┐  │
         │  │  LRU   │ │ LRU2 (Def)  │  │
         │  │Standard│ │Two-Level+Seg│  │
         │  └────────┘ └─────────────┘  │
         └──────────────────────────────┘
         ┌──────────────────────────────┐
         │       Distributed Layer       │
         │  ┌─────────┬────────┬──────┐ │
         │  │  Peer   │Consist.│ gRPC │ │
         │  │Awareness│ Hash   │Comm  │ │
         │  │+ Getter │+ etcd  │      │ │
         │  └─────────┴────────┴──────┘ │
         └──────────────────────────────┘
```

## Key Features

### Triple Protection

| Problem | Symptom | Solution |
|---------|---------|----------|
| **Cache Penetration** | Requests for non-existent keys hit DB | Bloom filter + null value caching |
| **Cache Stampede** | Hot key expires, concurrent requests flood DB | singleflight request merging |
| **Cache Avalanche** | Mass key expiration crashes DB | TTL random jitter (±10%)|

### Distributed Capabilities

- **gRPC Communication** — Protobuf + gRPC between nodes
- **etcd Service Discovery** — Auto-register on join, auto-deregister on leave
- **Consistent Hashing** — Virtual nodes for uniform distribution, auto-rebalance
- **Multi-node Sync** — Set/Delete concurrently syncs to all peers

### Storage Engines

- **LRU** — Standard Least Recently Used eviction
- **LRU2** (default) — Two-level cache (hot + warm data), segmented locks, high performance

## Quick Start

```bash
# Install dependencies
go mod download

# Build
go build ./...

# Run tests with race detection
go test -race ./...

# Run example (3 nodes)
go run example/test.go -port 8001 -node node1
go run example/test.go -port 8002 -node node2
go run example/test.go -port 8003 -node node3
```

### Makefile

```bash
make all       # format -> vet -> test -> build
make build     # build
make test      # run tests
make bench     # benchmarks (store package)
make cover     # coverage report (generates coverage.html)
make test-race # race detection
make fmt       # format code
make vet       # static analysis
make lint      # lint (requires golangci-lint)
make proto     # generate protobuf code
make deps      # update dependencies
make clean     # clean build artifacts
make example   # show example run commands
make check     # fmt + vet + lint + test
```

### API Usage

```go
// Create a cache group (namespace)
group := saokacache.NewGroup("users", 2<<20,
    saokacache.GetterFunc(func(ctx context.Context, key string) ([]byte, error) {
        // Load data on cache miss
        return fetchFromDatabase(ctx, key)
    }),
)

// Attach distributed peer discovery
picker, _ := saokacache.NewClientPicker(":8001")
group.RegisterPeers(picker)

// Start gRPC server
server, _ := saokacache.NewServer(":8001", "saoka-cache",
    saokacache.WithEtcdEndpoints([]string{"localhost:2379"}),
)

// Use the cache
val, err := group.Get(ctx, "user:123")
group.Set(ctx, "user:456", data, 5*time.Minute)
group.Delete(ctx, "user:789")
```

## Project Structure

```
.
├── group.go              # User API entry point (namespace)
├── policy.go             # Policy layer: anti-penetration/stampede/avalanche
├── cache.go              # Pure storage layer wrapper
├── byteview.go           # Immutable byte view
├── server.go             # gRPC server
├── client.go             # gRPC client
├── peers.go              # Consistent hashing + etcd discovery
├── utils.go              # Utility functions
├── store/
│   ├── store.go          # Store interface + factory
│   ├── lru.go            # Standard LRU
│   ├── lru2.go           # Two-level LRU (segmented locks)
│   └── lru2_test.go      # Unit tests + benchmarks
├── bloom/
│   ├── bloom.go          # Bloom filter
│   └── bloom_test.go
├── consistenthash/
│   ├── config.go         # Configuration
│   ├── con_hash.go       # Consistent hashing implementation
│   └── con_hash_test.go
├── singleflight/
│   ├── singleflight.go   # Request merging
│   └── singleflight_test.go
├── registry/
│   └── register.go       # etcd service registration
├── pb/
│   ├── kama.proto        # Protobuf definition
│   ├── kama.pb.go        # Generated code
│   └── kama_grpc.pb.go   # gRPC generated code
└── example/
    └── test.go           # Multi-node demo
```

## Testing

```bash
# All tests with race detection
go test -race ./...

# Specific packages
go test -v ./store/...
go test -v ./bloom/...
go test -v ./consistenthash/...
go test -v ./singleflight/...

# Benchmarks
go test -bench=. ./store/...

# Coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Tech Stack

| Dependency | Version | Purpose |
|------------|---------|---------|
| `google.golang.org/grpc` | v1.70.0 | gRPC communication |
| `google.golang.org/protobuf` | v1.36.4 | Protobuf serialization |
| `go.etcd.io/etcd/client/v3` | v3.5.18 | etcd service discovery |
| `github.com/sirupsen/logrus` | v1.9.3 | Structured logging |

## License

MIT License
