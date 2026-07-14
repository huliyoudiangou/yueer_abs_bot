# syntax=docker/dockerfile:1

# 第一阶段：编译环境
FROM golang:1.22-alpine3.20 AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# 先复制 go.mod，不强制要求宿主机有 go.sum
COPY go.mod ./

# 先下载 go.mod 中已有依赖
RUN go mod download

# 再复制完整源码
COPY . ./

# 在容器内自动生成/更新 go.sum
RUN go mod tidy

# SQLite 需要 CGO
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags '-extldflags "-static" -s -w' -o abs_bot .

# 第二阶段：运行环境
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata su-exec

# 创建非 root 用户
RUN addgroup -S app && adduser -S app -G app -u 10001

WORKDIR /app

COPY --from=builder /app/abs_bot /app/abs_bot
COPY docker-entrypoint.sh /app/docker-entrypoint.sh

RUN mkdir -p /app/data && chown -R app:app /app && chmod +x /app/docker-entrypoint.sh

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["./abs_bot"]
