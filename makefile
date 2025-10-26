# 定义变量
# proto文件路径
PROTO_FILE := proto/user.proto
# pb代码输出目录
PB_OUTPUT_DIR := user
# 主服务目录
SERVER_DIR := server
# proxy服务目录
PROXY_DIR := redis-proxy
# 主服务编译输出路径
SERVER_BIN := bin/chat-server
# proxy服务编译输出路径
PROXY_BIN := bin/redis-proxy
# 主服务PID文件（用于进程管理）
SERVER_PID := bin/server.pid
# proxy服务PID文件（用于进程管理）
PROXY_PID := bin/proxy.pid

# 检查必要工具是否存在（protoc及代码生成工具）
PROTOC := $(shell command -v protoc 2> /dev/null)
PROTOC_GEN_GO := $(shell command -v protoc-gen-go 2> /dev/null)
PROTOC_GEN_GO_GRPC := $(shell command -v protoc-gen-go-grpc 2> /dev/null)

# 默认目标：显示帮助信息
.DEFAULT_GOAL := help

# 帮助信息目标
# 显示所有可用命令及说明
help:
	@echo "可用命令:"
	@echo "  make proto      - 生成gRPC proto代码（依赖proto文件）"
	@echo "  make build      - 编译所有服务（主服务和proxy）"
	@echo "  make build-server - 仅编译主服务"
	@echo "  make build-proxy  - 仅编译proxy服务"
	@echo "  make run        - 启动所有服务（后台运行）"
	@echo "  make run-server - 仅启动主服务（前台运行，方便调试）"
	@echo "  make run-proxy  - 仅启动proxy服务（前台运行，方便调试）"
	@echo "  make stop       - 停止所有服务"
	@echo "  make clean      - 清理编译产物和生成的proto代码"

# 生成proto代码目标
# 依赖：检查工具是否安装 + proto源文件
# 功能：通过protoc生成Go语言的gRPC代码
proto: check-tools $(PROTO_FILE)
	@echo "生成proto代码..."
	@mkdir -p $(PB_OUTPUT_DIR)
	protoc --go_out=$(PB_OUTPUT_DIR) --go-grpc_out=$(PB_OUTPUT_DIR) $(PROTO_FILE)
	@echo "proto代码生成完成"

# 工具检查目标
# 功能：验证protoc、protoc-gen-go、protoc-gen-go-grpc是否安装，缺失则提示安装方法
check-tools:
	@if [ -z "$(PROTOC)" ]; then \
		echo "错误：未找到protoc，请先安装protobuf工具（https://grpc.io/docs/protoc-installation/）"; \
		exit 1; \
	fi
	@if [ -z "$(PROTOC_GEN_GO)" ]; then \
		echo "错误：未找到protoc-gen-go，请执行：go install google.golang.org/protobuf/cmd/protoc-gen-go@latest"; \
		exit 1; \
	fi
	@if [ -z "$(PROTOC_GEN_GO_GRPC)" ]; then \
		echo "错误：未找到protoc-gen-go-grpc，请执行：go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest"; \
		exit 1; \
	fi

# 全量编译目标
# 依赖：分别编译主服务和proxy服务
# 功能：编译所有服务并提示完成
build: build-server build-proxy
	@echo "所有服务编译完成"

# 主服务编译目标
# 依赖：proto生成的代码目录（确保代码已生成）
# 功能：编译主服务代码到bin目录
build-server: $(PB_OUTPUT_DIR)
	@echo "编译主服务..."
	@mkdir -p bin
	go build -o $(SERVER_BIN) $(SERVER_DIR)/main.go

# proxy服务编译目标
# 依赖：proto生成的代码目录（确保代码已生成）
# 功能：编译proxy服务代码到bin目录
build-proxy: $(PB_OUTPUT_DIR)
	@echo "编译redis-proxy服务..."
	@mkdir -p bin
	go build -o $(PROXY_BIN) $(PROXY_DIR)/main.go

# proto代码目录依赖目标
# 功能：若proto代码目录不存在，自动触发proto生成
$(PB_OUTPUT_DIR):
	@make proto

# 后台启动所有服务目标
# 依赖：先停止已有服务 + 重新编译服务
# 功能：后台启动proxy和主服务，记录PID到文件
run: stop build
	@echo "启动redis-proxy服务..."
	$(PROXY_BIN) & echo $$! > $(PROXY_PID)
	@sleep 1  # 等待proxy启动完成
	@echo "启动主服务..."
	$(SERVER_BIN) & echo $$! > $(SERVER_PID)
	@echo "所有服务已启动"

# 前台启动主服务目标（调试用）
# 依赖：主服务已编译
# 功能：在前台启动主服务，方便查看日志输出
run-server: build-server
	@echo "启动主服务（前台运行）..."
	$(SERVER_BIN)

# 前台启动proxy服务目标（调试用）
# 依赖：proxy服务已编译
# 功能：在前台启动proxy服务，方便查看日志输出
run-proxy: build-proxy
	@echo "启动redis-proxy服务（前台运行）..."
	$(PROXY_BIN)

# 停止所有服务目标
# 功能：通过PID文件停止主服务和proxy服务，清理PID文件
stop:
	@if [ -f $(SERVER_PID) ]; then \
		echo "停止主服务..."; \
		kill `cat $(SERVER_PID)` || true; \
		rm -f $(SERVER_PID); \
	fi
	@if [ -f $(PROXY_PID) ]; then \
		echo "停止redis-proxy服务..."; \
		kill `cat $(PROXY_PID)` || true; \
		rm -f $(PROXY_PID); \
	fi
	@echo "所有服务已停止"

# 清理目标
# 依赖：先停止所有服务
# 功能：删除编译产物（bin目录）和生成的proto代码（user目录）
clean: stop
	@echo "清理编译产物..."
	rm -rf bin
	@echo "清理proto生成代码..."
	rm -rf $(PB_OUTPUT_DIR)
	@echo "清理完成"