# =============================================
# Fast Strm Dockerfile
# 维护者: wabisabi926
# 说明: Go 多阶段构建，单二进制运行，零依赖
# =============================================

# ---------- 阶段1: 构建 Go 二进制 ----------
FROM golang:1.23-alpine AS builder

ENV TZ=Asia/Shanghai \
    GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=off \
    CGO_ENABLED=0

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETARCH=amd64
ARG VERSION=dev
ARG BUILD_DATE

RUN GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath \
    -ldflags="-s -w -X 'github.com/wabisabi926/faststrm/internal/handler.appVersion=${VERSION}' -X 'main.BuildDate=${BUILD_DATE}'" \
    -o /out/faststrm ./cmd/server/

# ---------- 阶段2: 生产运行 ----------
FROM alpine:3.19

ENV TZ=Asia/Shanghai \
    PATH=/app:$PATH

RUN apk add --no-cache tzdata ca-certificates wget && \
    cp /usr/share/zoneinfo/${TZ} /etc/localtime && \
    echo ${TZ} > /etc/timezone

WORKDIR /app

COPY --from=builder /out/faststrm .
COPY docker-entrypoint.sh .
COPY .config/ ./.config/
RUN chmod +x faststrm docker-entrypoint.sh

RUN addgroup -g 12331 faststrm && \
    adduser -D -u 12331 -G faststrm faststrm

VOLUME ["/app/config", "/app/data"]

EXPOSE 8090

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/faststrm"]
