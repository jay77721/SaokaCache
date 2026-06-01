# SaokaCache Makefile

.PHONY: all build test clean lint fmt vet bench cover proto help

# 默认目标
all: fmt vet test build

# 构建项目
build:
	@echo "构建项目..."
	go build ./...

# 运行所有测试
test:
	@echo "运行测试..."
	go test ./...

# 运行测试并显示详细输出
test-verbose:
	@echo "运行详细测试..."
	go test -v ./...

# 运行特定包的测试
test-store:
	@echo "运行 store 包测试..."
	go test ./store/...

# 运行性能测试
bench:
	@echo "运行性能测试..."
	go test -bench=. ./store/...

# 运行竞态检测
test-race:
	@echo "运行竞态检测..."
	go test -race ./...

# 生成测试覆盖率
cover:
	@echo "生成测试覆盖率..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "覆盖率报告已生成: coverage.html"

# 格式化代码
fmt:
	@echo "格式化代码..."
	go fmt ./...

# 静态分析
vet:
	@echo "运行静态分析..."
	go vet ./...

# 代码检查（需要安装 golangci-lint）
lint:
	@echo "运行代码检查..."
	golangci-lint run

# 生成 protobuf 代码
proto:
	@echo "生成 protobuf 代码..."
	protoc --go_out=. --go-grpc_out=. pb/kama.proto

# 清理构建产物
clean:
	@echo "清理构建产物..."
	go clean
	rm -f coverage.out coverage.html

# 更新依赖
deps:
	@echo "更新依赖..."
	go mod tidy
	go mod download

# 运行示例
example:
	@echo "运行示例（3 节点）..."
	@echo "请在不同终端运行以下命令:"
	@echo "go run example/test.go -port 8001 -node node1"
	@echo "go run example/test.go -port 8002 -node node2"
	@echo "go run example/test.go -port 8003 -node node3"

# 检查代码质量
check: fmt vet lint test
	@echo "代码质量检查完成！"

# 显示帮助
help:
	@echo "SaokaCache 开发命令:"
	@echo ""
	@echo "  make all          - 运行格式化、分析、测试和构建"
	@echo "  make build        - 构建项目"
	@echo "  make test         - 运行所有测试"
	@echo "  make test-verbose - 运行详细测试"
	@echo "  make test-store   - 运行 store 包测试"
	@echo "  make bench        - 运行性能测试"
	@echo "  make test-race    - 运行竞态检测"
	@echo "  make cover        - 生成测试覆盖率报告"
	@echo "  make fmt          - 格式化代码"
	@echo "  make vet          - 运行静态分析"
	@echo "  make lint         - 运行代码检查（需要 golangci-lint）"
	@echo "  make proto        - 生成 protobuf 代码"
	@echo "  make clean        - 清理构建产物"
	@echo "  make deps         - 更新依赖"
	@echo "  make example      - 显示运行示例的命令"
	@echo "  make check        - 运行完整的代码质量检查"
	@echo "  make help         - 显示此帮助信息"
