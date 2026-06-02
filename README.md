# SaokaCache

分布式内存缓存系统，Go 实现。支持 gRPC 通信、etcd 服务发现、三级缓存防护（防穿透/防击穿/防雪崩）。

> [English Documentation](README_EN.md)

---

## 架构

```
         ┌──────────────────────────────┐
         │        Group (用户 API)       │
         │   Get / Set / Delete / Stats  │
         └──────────────┬───────────────┘
                        │
         ┌──────────────▼───────────────┐
         │     CachePolicy (策略层)      │
         │  ┌─────────┬────────┬──────┐ │
         │  │ Bloom   │ Single │ TTL  │ │
         │  │ Filter  │ Flight │ Jitter│ │
         │  │(防穿透) │(防击穿)│(防雪崩)│ │
         │  └─────────┴────────┴──────┘ │
         └──────────────┬───────────────┘
                        │
         ┌──────────────▼───────────────┐
         │      Cache (纯存储层)         │
         │    lazy init / Get / Set     │
         └──────────────┬───────────────┘
                        │
         ┌──────────────▼───────────────┐
         │       Store 接口             │
         │  ┌────────┐ ┌─────────────┐  │
         │  │  LRU   │ │ LRU2 (默认)  │  │
         │  │标准淘汰 │ │二级缓存+分段锁│  │
         │  └────────┘ └─────────────┘  │
         └──────────────────────────────┘
         ┌──────────────────────────────┐
         │        分布式层               │
         │  ┌─────────┬────────┬──────┐ │
         │  │  Peer   │ 一致性  │ gRPC │ │
         │  │Awareness │ 哈希   │通信  │ │
         │  │+ Getter │+ etcd  │      │ │
         │  └─────────┴────────┴──────┘ │
         └──────────────────────────────┘
```

## 核心特性

### 三级防护

| 问题 | 现象 | 解决方案 |
|------|------|----------|
| **缓存穿透** | 请求不存在的 key，全部打到数据库 | Bloom 过滤器拦截 + 空值缓存 |
| **缓存击穿** | 热点 key 过期，大量并发同时回源 | singleflight 合并请求 |
| **缓存雪崩** | 大量 key 同时过期，数据库被压垮 | TTL 随机抖动（±10%）|

### 分布式能力

- **gRPC 通信** — 节点间通过 Protobuf + gRPC 交换数据
- **etcd 服务发现** — 新节点自动注册，下线自动摘除
- **一致性哈希** — 虚拟节点均匀分配，自动重平衡
- **多节点同步** — Set/Delete 操作并发同步到所有 peer

### 存储引擎

- **LRU** — 标准最近最少使用淘汰
- **LRU2**（默认）— 二级缓存（热数据 + 温数据），分段锁，高性能

## 快速开始

```bash
# 安装依赖
go mod download

# 构建
go build ./...

# 运行测试（含竞态检测）
go test -race ./...

# 运行示例（3 节点）
go run example/test.go -port 8001 -node node1
go run example/test.go -port 8002 -node node2
go run example/test.go -port 8003 -node node3
```

### Makefile

```bash
make all       # 格式化 -> 静态分析 -> 测试 -> 构建
make build     # 构建
make test      # 运行测试
make bench     # 性能测试（store 包）
make cover     # 覆盖率报告（生成 coverage.html）
make test-race # 竞态检测
make fmt       # 代码格式化
make vet       # 静态分析
make lint      # 代码检查（需 golangci-lint）
make proto     # 生成 protobuf 代码
make deps      # 更新依赖
make clean     # 清理构建产物
make example   # 显示运行示例的命令
make check     # fmt + vet + lint + test
```

### API 使用示例

```go
// 创建一个缓存组（命名空间）
group := saokacache.NewGroup("users", 2<<20,
    saokacache.GetterFunc(func(ctx context.Context, key string) ([]byte, error) {
        // 缓存未命中时的回源逻辑
        return fetchFromDatabase(ctx, key)
    }),
)

// 绑定分布式节点
picker, _ := saokacache.NewClientPicker(":8001")
group.RegisterPeers(picker)

// 启动 gRPC 服务
server, _ := saokacache.NewServer(":8001", "saoka-cache",
    saokacache.WithEtcdEndpoints([]string{"localhost:2379"}),
)

// 使用
val, err := group.Get(ctx, "user:123")
group.Set(ctx, "user:456", data, 5*time.Minute)
group.Delete(ctx, "user:789")
```

## 项目结构

```
.
├── group.go              # 用户 API 入口（命名空间）
├── policy.go             # 策略层：防穿透/击穿/雪崩
├── cache.go              # 纯存储层封装
├── byteview.go           # 不可变字节视图
├── server.go             # gRPC 服务端
├── client.go             # gRPC 客户端
├── peers.go              # 一致性哈希 + etcd 服务发现
├── utils.go              # 工具函数
├── store/
│   ├── store.go          # Store 接口 + 工厂
│   ├── lru.go            # 标准 LRU
│   ├── lru2.go           # 二级 LRU（分段锁）
│   └── lru2_test.go      # 单元测试 + 基准测试
├── bloom/
│   ├── bloom.go          # Bloom 过滤器
│   └── bloom_test.go
├── consistenthash/
│   ├── config.go         # 配置
│   ├── con_hash.go       # 一致性哈希实现
│   └── con_hash_test.go
├── singleflight/
│   ├── singleflight.go   # 请求合并
│   └── singleflight_test.go
├── registry/
│   └── register.go       # etcd 服务注册
├── pb/
│   ├── kama.proto        # Protobuf 定义
│   ├── kama.pb.go        # 生成代码
│   └── kama_grpc.pb.go   # gRPC 生成代码
└── example/
    └── test.go           # 多节点演示
```

## 测试

```bash
# 全部测试（含竞态检测）
go test -race ./...

# 指定包
go test -v ./store/...
go test -v ./bloom/...
go test -v ./consistenthash/...
go test -v ./singleflight/...

# 性能测试
go test -bench=. ./store/...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 技术栈

| 依赖 | 版本 | 用途 |
|------|------|------|
| `google.golang.org/grpc` | v1.70.0 | gRPC 通信 |
| `google.golang.org/protobuf` | v1.36.4 | Protobuf 序列化 |
| `go.etcd.io/etcd/client/v3` | v3.5.18 | etcd 服务发现 |
| `github.com/sirupsen/logrus` | v1.9.3 | 结构化日志 |

## 许可

MIT License
