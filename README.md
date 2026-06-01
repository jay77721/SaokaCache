# SaokaCache

分布式内存缓存系统（Go），支持 gRPC 通信、etcd 服务发现、防穿透/击穿/雪崩机制。

> **[English Documentation](README_EN.md)**

## 架构

```
┌─────────────────────────────────────────────┐
│              用户 API (Group)                │
│         Get / Set / Delete / Stats           │
└──────────────────┬──────────────────────────┘
                   ▼
┌─────────────────────────────────────────────┐
│           CachePolicy (策略层)               │
│  ┌───────────┐ ┌────────────┐ ┌──────────┐  │
│  │ 布隆过滤器 │ │singleflight│ │ TTL 抖动 │  │
│  │ (防穿透)   │ │ (防击穿)   │ │ (防雪崩) │  │
│  └───────────┘ └────────────┘ └──────────┘  │
└──────────────────┬──────────────────────────┘
                   ▼
┌─────────────────────────────────────────────┐
│             Cache (纯存储层)                 │
│       lazy init / Get / Set / Stats          │
└──────────────────┬──────────────────────────┘
                   ▼
┌─────────────────────────────────────────────┐
│              Store 接口                      │
│  ┌──────────┐       ┌──────────────────┐    │
│  │   LRU    │       │  LRU2 (默认)     │    │
│  │ 标准淘汰  │       │ 二级缓存+分段锁  │    │
│  └──────────┘       └──────────────────┘    │
└─────────────────────────────────────────────┘

┌─────────────────────────────────────────────┐
│              分布式层                        │
│  ┌──────────────┐ ┌─────────┐ ┌──────────┐  │
│  │peerAwareGetter│ │ 一致性  │ │  gRPC    │  │
│  │ peer+getter  │ │  哈希   │ │Server/   │  │
│  │              │ │ + etcd  │ │Client    │  │
│  └──────────────┘ └─────────┘ └──────────┘  │
└─────────────────────────────────────────────┘
```

## 核心特性

### 三重防护

| 问题 | 现象 | 解决方案 |
|------|------|----------|
| **缓存穿透** | 请求不存在的 key，全部打到数据库 | 布隆过滤器拦截 + 空值缓存 |
| **缓存击穿** | 热点 key 过期，大量并发同时回源 | singleflight 合并请求 |
| **缓存雪崩** | 大量 key 同时过期，数据库被压垮 | TTL 随机抖动（±10%） |

### 分布式能力

- **gRPC 通信** — 节点间通过 Protobuf + gRPC 交换数据
- **etcd 服务发现** — 新节点自动注册，下线自动摘除
- **一致性哈希** — 虚拟节点均匀分配，自动重平衡
- **多节点同步** — Set/Delete 操作并发同步到所有 peer

### 存储引擎

- **LRU** — 标准最近最少使用淘汰
- **LRU2** — 二级缓存（热数据 + 温数据），分段锁，高性能

## 快速开始

```bash
# 安装依赖
go mod download

# 构建
go build ./...

# 运行测试
go test -race ./...

# 运行示例（3 节点）
go run example/test.go -port 8001 -node node1
go run example/test.go -port 8002 -node node2
go run example/test.go -port 8003 -node node3
```

### 使用 Makefile

```bash
make build    # 构建
make test     # 测试
make bench    # 性能测试
make cover    # 覆盖率报告
make vet      # 静态分析
```

## 项目结构

```
.
├── group.go              # 用户 API 入口
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
│   └── lru2_test.go      # 单元测试 + 性能测试
├── bloom/
│   ├── bloom.go          # 布隆过滤器
│   └── bloom_test.go     # 单元测试
├── consistenthash/
│   ├── config.go         # 配置
│   ├── con_hash.go       # 一致性哈希实现
│   └── con_hash_test.go  # 单元测试
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
# 运行所有测试（含竞态检测）
go test -race ./...

# 运行特定包测试
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

## 许可证

MIT License

## 贡献（Contributing）

欢迎贡献！推荐的贡献流程：

```bash
# Fork 仓库并克隆
git clone git@github.com:<your-username>/SaokaCache.git
cd SaokaCache

# 创建 feature 分支
git checkout -b feat/your-feature

# 做出改动并提交
git add .
git commit -m "feat: 描述你的改动"

# 推送到你仓库的分支
git push origin feat/your-feature

# 在 GitHub 上打开 Pull Request，描述变更和测试步骤
```

如果你直接对上游仓库有写权限，请在提交前确保分支与 `main` 同步并通过所有测试：

```bash
git fetch origin
git checkout main
git pull origin main
git checkout feat/your-feature
git rebase main
go test ./...
```

感谢你的贡献！
