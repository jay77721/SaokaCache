# SaokaCache

A distributed in-memory cache system written in Go, featuring gRPC communication, etcd service discovery, and anti-penetration/stampede/avalanche mechanisms.

> **[中文文档](README.md)**

## Architecture

```
┌─────────────────────────────────────────────┐
│              User API (Group)               │
│         Get / Set / Delete / Stats           │
└──────────────────┬──────────────────────────┘
                   ▼
┌─────────────────────────────────────────────┐
│          CachePolicy (Policy Layer)          │
│  ┌───────────┐ ┌────────────┐ ┌──────────┐  │
│  │   Bloom   │ │singleflight│ │ TTL Jitter│  │
│  │  Filter   │ │ (Stampede) │ │(Avalanche)│  │
│  │(Penetrate)│ │            │ │           │  │
│  └───────────┘ └────────────┘ └──────────┘  │
└──────────────────┬──────────────────────────┘
                   ▼
┌─────────────────────────────────────────────┐
│           Cache (Pure Storage Layer)         │
│       lazy init / Get / Set / Stats          │
└──────────────────┬──────────────────────────┘
                   ▼
┌─────────────────────────────────────────────┐
│              Store Interface                 │
│  ┌──────────┐       ┌──────────────────┐    │
│  │   LRU    │       │  LRU2 (Default)  │    │
│  │ Standard │       │ Two-Level Cache  │    │
│  └──────────┘       │ Segmented Lock   │    │
│                     └──────────────────┘    │
└─────────────────────────────────────────────┘

┌─────────────────────────────────────────────┐
│           Distributed Layer                  │
│  ┌──────────────┐ ┌─────────┐ ┌──────────┐  │
│  │peerAwareGetter│ │Consistent│ │  gRPC    │  │
│  │ peer+getter  │ │  Hash   │ │Server/   │  │
│  │              │ │ + etcd  │ │Client    │  │
│  └──────────────┘ └─────────┘ └──────────┘  │
└─────────────────────────────────────────────┘
```

## Key Features

### Triple Protection

| Problem | Symptom | Solution |
|---------|---------|----------|
| **Cache Penetration** | Requests for non-existent keys hit DB | Bloom filter + null value caching |
| **Cache Stampede** | Hot key expires, concurrent requests flood DB | singleflight request merging |
| **Cache Avalanche** | Mass key expiration crashes DB | TTL random jitter (±10%) |

### Distributed Capabilities

- **gRPC Communication** — Protobuf + gRPC between nodes
- **etcd Service Discovery** — Auto-register on join, auto-deregister on leave
- **Consistent Hashing** — Virtual nodes for uniform distribution, auto-rebalance
- **Multi-node Sync** — Set/Delete concurrently syncs to all peers

### Storage Engines

- **LRU** — Standard Least Recently Used eviction
- **LRU2** — Two-level cache (hot + warm data), segmented locks, high performance

## Quick Start

```bash
# Install dependencies
go mod download

# Build
go build ./...

# Run tests
go test -race ./...

# Run example (3 nodes)
go run example/test.go -port 8001 -node node1
go run example/test.go -port 8002 -node node2
go run example/test.go -port 8003 -node node3
```

### Using Makefile

```bash
make build    # Build
make test     # Run tests
make bench    # Benchmark
make cover    # Coverage report
make vet      # Static analysis
```

## Project Structure

```
.
├── group.go              # User API entry point
├── policy.go             # Policy layer: anti-penetration/stampede/avalanche
├── cache.go              # Pure storage layer wrapper
├── byteview.go           # Immutable byte view
├── server.go             # gRPC server
├── client.go             # gRPC client
├── peers.go              # Consistent hashing + etcd service discovery
├── utils.go              # Utility functions
├── store/
│   ├── store.go          # Store interface + factory
│   ├── lru.go            # Standard LRU
│   ├── lru2.go           # Two-level LRU (segmented locks)
│   └── lru2_test.go      # Unit tests + benchmarks
├── bloom/
│   ├── bloom.go          # Bloom filter
│   └── bloom_test.go     # Unit tests
├── consistenthash/
│   ├── config.go         # Configuration
│   ├── con_hash.go       # Consistent hashing implementation
│   └── con_hash_test.go  # Unit tests
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
# Run all tests with race detection
go test -race ./...

# Run specific package tests
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
