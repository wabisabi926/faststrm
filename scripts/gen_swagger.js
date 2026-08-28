#!/usr/bin/env node
/**
 * gen_swagger.js — 从 routes.go 解析路由，生成 swagger.json
 * 用法: node scripts/gen_swagger.js
 * 输出: internal/server/swagger.json
 *
 * 这个脚本是 P2 "API Reference 自动生成" 的实现：
 *  - 解析 routes.go 里所有 {Method: http.MethodXXX, Path: "..."} 路由
 *  - 自动按 path 前缀打 tag
 *  - 标记公开/受保护路由（publicPaths 硬编码）
 *  - 生成 openapi 3.0.3 swagger.json
 */
const fs = require('fs');
const path = require('path');

const ROUTES_GO = path.resolve(__dirname, '../internal/server/routes.go');
const OUTPUT = path.resolve(__dirname, '../internal/server/swagger.json');

const content = fs.readFileSync(ROUTES_GO, 'utf8');

// 匹配所有 {Method: http.MethodXXX, Path: "/xxx", ...} 结构
const routeRegex = /Method:\s*http\.Method(\w+)\s*,\s*Path:\s*"([^"]+)"/g;
const routes = [];
let m;
while ((m = routeRegex.exec(content)) !== null) {
  routes.push({ method: m[1].toLowerCase(), path: m[2] });
}

const tags = [
  { name: 'Public',     description: '公开接口（无需 JWT）' },
  { name: 'Auth',       description: '认证登录' },
  { name: 'STRM',       description: 'STRM 代理 & 转发' },
  { name: 'Account',    description: '115 账号管理' },
  { name: 'Task',       description: '任务管理 & 执行' },
  { name: 'Directory',  description: '目录浏览 & 清理' },
  { name: 'Notify',     description: 'Telegram / Emby 通知' },
  { name: 'Settings',   description: '全局设置' },
  { name: 'History',    description: '历史记录' },
  { name: 'System',     description: '系统功能' },
];

// 硬编码的公开路由（见 routes.go publicRoutes 分组）
const publicPaths = new Set([
  '/api/health', '/api/auth/login', '/api/auth/change-password',
  '/api/auth/change-credentials', '/api/auth/logout',
  '/api/events/stream', '/api/emby/webhook', '/api/notify/webhook',
  '/api/strm', '/api/fs/get',
]);

function pathToTag(p) {
  if (p.startsWith('/api/health') || p.startsWith('/api/events/stream')) return 'Public';
  if (p.startsWith('/api/auth')) return 'Auth';
  if (p.startsWith('/api/strm') || p.startsWith('/api/fs/get')) return 'STRM';
  if (p.startsWith('/api/account')) return 'Account';
  if (p.startsWith('/api/task') || p.startsWith('/api/startTask') || p.startsWith('/api/cancelTask')) return 'Task';
  if (p.startsWith('/api/taskHistory')) return 'History';
  if (p.startsWith('/api/history')) return 'History';
  if (p.startsWith('/api/directory')) return 'Directory';
  if (p.startsWith('/api/notify')) return 'Notify';
  if (p.startsWith('/api/emby')) return 'Settings';
  if (p.startsWith('/api/settings')) return 'Settings';
  if (p.startsWith('/api/mediaMountSync')) return 'System';
  if (p.startsWith('/api/strmCleanup')) return 'System';
  if (p.startsWith('/api/lifeMonitor') || p.startsWith('/api/lifeEvents')) return 'System';
  return 'System';
}

const paths = {};
for (const r of routes) {
  if (!paths[r.path]) paths[r.path] = {};
  const tag = pathToTag(r.path);
  const isPublic = publicPaths.has(r.path);
  paths[r.path][r.method] = {
    tags: [tag],
    summary: `${r.method.toUpperCase()} ${r.path}`,
    security: isPublic ? [] : [{ bearerAuth: [] }],
    responses: {
      '200': { description: '成功' },
      '401': { description: '未授权 (JWT 缺失或无效)' },
      '500': { description: '服务器错误' }
    }
  };
}

const swagger = {
  openapi: '3.0.3',
  info: {
    title: 'faststrm API',
    version: 'v1.1.1',
    description: 'faststrm 后端 REST API 文档\n\n基于 go-zero rest 框架，JWT Bearer Token 认证。\nSwagger UI: /api/docs/ui'
  },
  servers: [{ url: 'http://localhost:8090', description: '默认端口' }],
  tags,
  components: {
    securitySchemes: {
      bearerAuth: { type: 'http', scheme: 'bearer', bearerFormat: 'JWT', description: '登录后返回的 JWT Token' }
    }
  },
  paths
};

fs.writeFileSync(OUTPUT, JSON.stringify(swagger, null, 2));
console.log(`[gen_swagger] ✓ ${routes.length} routes → ${path.relative(process.cwd(), OUTPUT)}`);
