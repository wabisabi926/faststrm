# =============================================
# OpenStrm Dockerfile
# 维护者: wabisabi926
# 说明: 多阶段构建，生产环境使用
# =============================================

# ---------- 阶段1: 构建前端 ----------
FROM node:22-alpine AS builder
WORKDIR /app/frontend

# 安装构建依赖
COPY frontend/package*.json ./
COPY frontend/yarn.lock ./
RUN yarn install --frozen-lockfile

# 构建前端
COPY frontend/ ./
RUN yarn build

# ---------- 阶段2: 生产运行 ----------
FROM node:22-alpine AS runner
WORKDIR /app

# 安装 nginx (alpine 自带 njs 模块)
RUN apk add --no-cache nginx nginx-mod-http-js tzdata

# 设置时区
ENV TZ=Asia/Shanghai
RUN cp /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

# 拷贝 Next.js standalone 产物
COPY --from=builder /app/frontend/.next/standalone ./frontend
COPY --from=builder /app/frontend/.next/static ./frontend/.next/static
COPY --from=builder /app/frontend/public ./frontend/public

# 拷贝 nginx 配置
COPY emby2Alist/nginx/nginx.conf /etc/nginx/nginx.conf
COPY emby2Alist/nginx/conf.d /etc/nginx/conf.d

# 拷贝默认配置
COPY .config /app/.config

# 创建必要目录
RUN mkdir -p /var/cache/nginx/emby/images \
    /var/cache/nginx/emby/subtitles \
    /var/cache/nginx/client_temp \
    /var/log/nginx \
    /app/config

# 拷贝启动脚本
COPY docker-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# 健康检查
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:3000/ || exit 1

# 暴露端口: 3000(前端UI), 8091(emby接口)
EXPOSE 3000 8091

# 环境变量
ENV NODE_ENV=production
ENV PORT=8000
ENV HOSTNAME=0.0.0.0

# 启动入口
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["node", "/app/frontend/server.js"]
