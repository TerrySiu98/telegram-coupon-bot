# 构建阶段（支持 CGO 和 SQLite）
FROM golang:1.23-bullseye AS builder

WORKDIR /app

# 安装 SQLite 构建依赖
RUN apt-get update && apt-get install -y \
    gcc \
    libc6-dev \
    libsqlite3-dev

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 启用 CGO，但不手动设置 GOARCH，让 buildx 自动处理
ENV CGO_ENABLED=1

# 打印详细构建日志，方便调试
RUN go build -v -x -o telegram-coupon-bot .

# 运行阶段（精简）
FROM debian:bullseye-slim

WORKDIR /app

# 安装运行时依赖
RUN apt-get update && apt-get install -y \
    libsqlite3-0 \
    ca-certificates && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# 拷贝编译好的程序和资源
COPY --from=builder /app/telegram-coupon-bot /app/
COPY templates/ /app/templates/

EXPOSE 5656

CMD ["./telegram-coupon-bot"]