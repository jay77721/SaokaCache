# CLAUDE.md - SaokaCache 开发指南

## 项目概述

SaokaCache 是一个用 Go 语言编写的分布式内存缓存系统，具备以下核心特性：
- gRPC 服务层
- 基于 etcd 的服务发现
- 反缓存穿透机制（布隆过滤器）
- 反缓存击穿机制（singleflight）
- 防缓存雪崩机制（TTL 抖动）

**技术栈**: Go 1.22+, gRPC, Protobuf, etcd, logrus

## 开发环境要求

### 必需工具
- Go 1.22 或更高版本
- Protocol Buffers 编译器 (`protoc`)
- Go protobuf 插件:
  - `protoc-gen-go`
  - `protoc-gen-go-grpc`

### 可选工具
- etcd (用于服务发现测试)
- grpcurl (用于 gRPC 接口调试)

### 安装依赖
```bash
# 安装 Go 依赖
go mod download

# 安装 protobuf 编译器 (macOS)
brew install protobuf

# 安装 Go protobuf 插件
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

## 快速开始

### 构建项目
```bash
# 构建所有包
go build ./...

# 构建并检查错误
go vet ./...
```

### 使用 Makefile（推荐）
```bash
# 查看所有可用命令
make help

# 运行完整的开发流程（格式化、分析、测试、构建）
make all

# 构建项目
make build

# 运行测试
make test

# 生成测试覆盖率报告
make cover

# 运行代码质量检查
make check
```

### 手动运行测试
```bash
# 运行所有测试
go test ./...

# 运行测试并显示详细输出
go test -v ./...

# 运行特定包的测试
go test ./store/...

# 运行性能测试
go test -bench=. ./store/...

# 运行竞态检测
go test -race ./...

# 生成测试覆盖率报告
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### 生成 Protobuf 代码
```bash
protoc --go_out=. --go-grpc_out=. pb/kama.proto
```

### 运行示例
```bash
# 启动多节点示例
go run example/test.go -port 8001 -node node1
go run example/test.go -port 8002 -node node2
go run example/test.go -port 8003 -node node3
```

## 项目结构

```
.
├── group.go              # 核心：缓存命名空间，提供 Get/Set/Delete API
├── cache.go              # 缓存层：布隆过滤器 + singleflight + Store
├── byteview.go           # 不可变字节切片包装器（所有方法返回副本）
├── client.go             # gRPC 客户端，实现 Peer 接口
├── server.go             # gRPC 服务器，包装缓存组
├── peers.go              # PeerPicker + ClientPicker，etcd 服务发现
├── utils.go              # 节点地址验证工具
├── bloom/                # 布隆过滤器（防缓存穿透）
│   └── bloom.go
├── consistenthash/       # 一致性哈希（虚拟节点 + 自动重平衡）
│   ├── config.go
│   └── con_hash.go
├── singleflight/         # 请求去重（防缓存击穿）
│   └── singleflight.go
├── store/                # 存储接口 + LRU/LRU2 实现
│   ├── store.go          # Store 工厂（LRU, LRU2）
│   ├── lru.go            # 标准 LRU（使用 container/list）
│   ├── lru2.go           # 二级 LRU（分段锁 + 自定义链表）
│   └── lru2_test.go      # 单元测试 + 性能测试
├── registry/             # etcd 服务注册（租约 + 心跳）
│   └── register.go
├── pb/                   # Protobuf 定义 + 生成代码
│   ├── kama.proto
│   ├── kama.pb.go
│   └── kama_grpc.pb.go
├── example/              # 多节点演示入口
│   └── test.go
├── go.mod
├── go.sum
└── README.md
```

## 架构说明

### 核心组件

1. **Group** - 用户 API 入口
   - 命名缓存空间
   - 通过 `Getter` 回调处理缓存未命中
   - 支持过期时间配置
   - 提供统计信息

2. **Cache** - 缓存层
   - 集成布隆过滤器（防穿透）
   - 集成 singleflight（防击穿）
   - TTL 抖动防雪崩
   - 支持多种淘汰策略

3. **Store** - 存储接口
   - `LRU`: 标准 LRU 淘汰
   - `LRU2`: 二级缓存，分段锁，高性能

4. **PeerPicker** - 分布式节点选择
   - 一致性哈希算法
   - 虚拟节点支持
   - 自动重平衡机制

5. **Server/Client** - gRPC 通信
   - Protobuf 序列化
   - 流式传输支持
   - 错误处理和重试

### 数据流

```
客户端请求
    ↓
Group.Get()
    ↓
[检查本地缓存] → 命中 → 返回
    ↓ 未命中
[检查布隆过滤器] → 可能不存在 → 返回空
    ↓ 可能存在
[检查远程节点] → 命中 → 返回
    ↓ 未命中
[调用 Getter 回调] → 返回数据并缓存
```

## 代码规范

### 语言要求
- **注释、错误消息、日志输出**：必须使用**中文**
- **代码结构**：遵循 Go 官方规范
- **命名**：驼峰命名法，首字母大写表示导出

### 设计模式

#### 函数选项模式
所有可配置类型使用函数选项模式：
```go
type XxxOption func(*Xxx)

func WithXxx(value Type) XxxOption {
    return func(x *Xxx) {
        x.xxx = value
    }
}

func NewXxx(opts ...XxxOption) *Xxx {
    x := &Xxx{
        // 默认值
    }
    for _, opt := range opts {
        opt(x)
    }
    return x
}
```

#### 错误处理
包级别定义错误变量：
```go
var (
    ErrKeyRequired   = errors.New("key is required")
    ErrValueRequired = errors.New("value is required")
    ErrGroupClosed   = errors.New("cache group is closed")
)
```

#### 不可变性
`ByteView` 是不可变的，所有公共方法返回副本：
```go
func (v ByteView) ByteSlice() []byte {
    b := make([]byte, len(v.b))
    copy(b, v.b)
    return b
}
```

#### 并发控制
- 使用 `atomic` 实现无锁快速路径
- 使用 `sync.Mutex` 保护存储操作
- 使用 singleflight 实现请求去重

### 日志规范
使用 logrus，添加 `[SaokaCache]` 前缀：
```go
logrus.WithFields(logrus.Fields{
    "key":   key,
    "group": g.name,
}).Info("[SaokaCache] 缓存命中")
```

### 测试规范
- 使用标准 `testing` 包
- 使用子测试 `t.Run()`
- 测试注释使用中文
- 覆盖率要求 80% 以上
- 包含性能基准测试

## 核心依赖

| 依赖 | 版本 | 用途 |
|---|---|---|
| `google.golang.org/grpc` | v1.70.0 | gRPC 服务器/客户端 |
| `google.golang.org/protobuf` | v1.36.4 | Protobuf 序列化 |
| `go.etcd.io/etcd/client/v3` | v3.5.18 | etcd 服务发现 |
| `github.com/sirupsen/logrus` | v1.9.3 | 结构化日志 |

## 开发工作流

### 添加新的存储实现

1. 创建 `store/xxx.go` 实现 `Store` 接口
2. 在 `store/store.go` 的 `NewStore()` 工厂函数中注册
3. 创建 `store/xxx_test.go` 编写测试
4. 参考 `store/lru2.go` 的实现模式：
   - 分段锁设计
   - 基准测试覆盖
   - 中文注释

### 添加新的 RPC 接口

1. 在 `pb/kama.proto` 中定义方法
2. 重新生成 protobuf 代码：
   ```bash
   protoc --go_out=. --go-grpc_out=. pb/kama.proto
   ```
3. 在 `server.go` 中实现服务端处理
4. 在 `client.go` 中实现客户端调用
5. 在 `Group` 或 `Cache` 层集成

### 代码审查检查清单

- [ ] 代码可读且命名良好
- [ ] 函数聚焦（<50 行）
- [ ] 文件内聚（<800 行）
- [ ] 无深层嵌套（>4 层）
- [ ] 错误显式处理
- [ ] 无硬编码值（使用常量或配置）
- [ ] 使用不可变模式
- [ ] 注释使用中文
- [ ] 测试覆盖率达到 80%+

## 性能优化要点

### LRU2 实现细节
- 分段桶设计，每个桶独立加锁
- 自定义链表，使用数组索引代替指针
- 自定义时钟，每 100ms 更新一次
- 二级缓存（热数据 + 温数据）

### 一致性哈希优化
- 虚拟节点提高均匀性
- 自动重平衡机制
- 每秒检查不平衡度（>25% 触发重平衡）

### 并发性能
- 原子操作实现无锁快速路径
- 请求去重减少后端压力
- 分段锁减少锁竞争

## 注意事项

### 关键限制
- LRU2 每桶容量限制为 `uint16`（65535 项）
- 自定义时钟每 100ms 更新一次（非实时）
- etcd 租约 TTL 为 10 秒（节点发现延迟）
- 一致性哈希重平衡每秒执行一次

### 常见陷阱
- 不要在 `ByteView` 上直接修改数据
- 不要在锁内执行耗时操作
- 不要忽略 singleflight 的错误处理
- 不要在测试中依赖时序

### 调试技巧
- 使用 `go test -race` 检测竞态条件
- 使用 `go test -v` 查看详细测试输出
- 使用 `go test -bench` 进行性能分析
- 使用 grpcurl 测试 gRPC 接口

## 相关资源

- [Go 语言规范](https://go.dev/ref/spec)
- [gRPC Go 文档](https://grpc.io/docs/languages/go/)
- [Protocol Buffers 文档](https://protobuf.dev/)
- [etcd 文档](https://etcd.io/docs/)

## 更新日志

### v2.0 (当前版本)
- 新增 LRU2 二级缓存实现
- 优化一致性哈希算法
- 增强错误处理和日志
- 完善测试覆盖率

### v1.0
- 基础缓存功能
- gRPC 服务层
- etcd 服务发现
- 布隆过滤器和 singleflight
