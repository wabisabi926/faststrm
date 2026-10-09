# =============================================
# Fast Strm Dockerfile
# 维护者: wabisabi926
# 说明: Go 多阶段构建，单二进制运行，零依赖
# =============================================

# ---------- 阶段1: 构建 Go 二进制 ----------
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ENV TZ=Asia/Shanghai \
    CGO_ENABLED=0

# GOPROXY 默认使用官方海外代理；国内构建可通过 --build-arg 覆盖，例如
#   docker build --build-arg GOPROXY=https://goproxy.cn,direct .
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} GOSUMDB=off

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG BUILD_DATE

RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
    -ldflags="-s -w \
      -X 'github.com/wabisabi926/faststrm/internal/handler.appVersion=${VERSION}' \
      -X 'github.com/wabisabi926/faststrm/internal/web.appVersion=${VERSION}' \
      -X 'main.version=${VERSION}' \
      -X 'main.BuildDate=${BUILD_DATE}'" \
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
# 防御性 CRLF → LF 转换：即使 .gitattributes 未生效（Windows clone autocrlf=true），也保证入口脚本是 LF
RUN sed -i 's/\r$//' docker-entrypoint.sh && chmod +x faststrm docker-entrypoint.sh

# 架构自检门禁：核对运行架构与 /app/faststrm 二进制 ELF 机器码一致，防止误装错架构镜像。
# 注意：CI 用 buildx 只产出 linux/amd64 + linux/arm64（见 release.yml，飞牛 fNOS 亦仅支持这两者），
# 因此下方 armv7|armhf 分支在 CI 流水线中实际永远不会命中（死分支）；
# 仅当有人手动用 GOARCH=arm 本地交叉构建出 arm 32 位二进制时才有兜底作用，这里保留以便该场景同样受保护，勿删。
RUN set -eu; \
    runtime_arch="$(apk --print-arch)"; \
    elf_machine="$(od -An -tu1 -j18 -N1 /app/faststrm | tr -dc '0-9')"; \
    case "${runtime_arch}" in \
      x86_64)      expect=62 ;; \
      aarch64)     expect=183 ;; \
      armv7|armhf) expect=40 ;; \
      *) echo "arch gate: unsupported runtime arch ${runtime_arch}"; exit 1 ;; \
    esac; \
    if [ "${elf_machine}" != "${expect}" ]; then \
      echo "arch gate FAILED: runtime ${runtime_arch} expects ELF e_machine ${expect}, got ${elf_machine} for /app/faststrm"; \
      exit 1; \
    fi; \
    echo "arch gate passed: ${runtime_arch} ELF e_machine=${elf_machine}"

RUN addgroup -g 12331 faststrm && \
    adduser -D -u 12331 -G faststrm faststrm

VOLUME ["/app/config", "/app/data"]

# 8090 = FastStrm 主应用端口（UI + API）
# 8097 = Emby 反代端口（用户在 UI 里启用 ProxyPort 后动态监听）
#        如果用户改了 ProxyPort（比如 8098/9000），需要在 docker-compose.yml 同步加端口映射
EXPOSE 8090 8097 8098 8099

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/faststrm"]
