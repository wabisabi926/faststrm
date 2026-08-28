// axios mock 工具：测试中替换 @/lib/axios 的 axiosInstance。
// 提供 mockGet/mockPost/reset 简化用例样板。
// 详见 v1.1.1 改进任务清单 T2。

import { vi, type Mock } from "vitest";
import type { AxiosInstance, AxiosRequestConfig, AxiosResponse, InternalAxiosRequestConfig } from "axios";

// 真实 axios 模块的拦截器会注入 Authorization 头，这里直接返回合并后的 config
type ResponseData = unknown;

interface MockRoute {
  method: "get" | "post" | "put" | "delete" | "patch";
  url: string | RegExp;
  handler: (config?: AxiosRequestConfig) => ResponseData | Promise<ResponseData>;
  status?: number;
}

interface MockCall {
  url: string;
  data?: unknown;
  params?: unknown;
}

// 构造一个可断言的 axiosInstance mock
export interface MockAxiosInstance {
  get: Mock;
  post: Mock;
  put: Mock;
  delete: Mock;
  patch: Mock;
  // 注册路由（按匹配顺序优先级，先注册先匹配）
  mockGet: (url: string | RegExp, handler: MockRoute["handler"]) => void;
  mockPost: (url: string | RegExp, handler: MockRoute["handler"]) => void;
  mockPut: (url: string | RegExp, handler: MockRoute["handler"]) => void;
  mockDelete: (url: string | RegExp, handler: MockRoute["handler"]) => void;
  // 通用重置（用例间调用）
  reset: () => void;
  // 取最近一次调用（用于断言）
  calls: MockCall[];
}

function makeResponse(
  data: ResponseData,
  status = 200,
  config?: InternalAxiosRequestConfig
): AxiosResponse {
  return {
    data,
    status,
    statusText: status === 200 ? "OK" : "Error",
    headers: {},
    config: config || ({ headers: {} } as InternalAxiosRequestConfig),
  };
}

function matchUrl(routeUrl: string | RegExp, actual: string): boolean {
  if (typeof routeUrl === "string") {
    return routeUrl === actual;
  }
  return routeUrl.test(actual);
}

function buildRouteMap() {
  const routes: Record<string, MockRoute[]> = { get: [], post: [], put: [], delete: [], patch: [] };
  return routes;
}

export function createMockAxiosInstance(): MockAxiosInstance {
  const routes = buildRouteMap();
  const calls: MockCall[] = [];

  function makeMethod(method: "get" | "post" | "put" | "delete" | "patch"): Mock {
    return vi.fn(async (url: string, data?: unknown, config?: AxiosRequestConfig) => {
      const call: MockCall = { url, data, params: config?.params };
      calls.push(call);
      const route = routes[method].find((r) => matchUrl(r.url, url));
      if (!route) {
        throw new Error(`[mockAxios] 未注册 ${method.toUpperCase()} ${url}`);
      }
      const result = await route.handler({ ...config, data });
      return makeResponse(result, route.status, {} as InternalAxiosRequestConfig);
    });
  }

  function register(method: "get" | "post" | "put" | "delete" | "patch") {
    return (url: string | RegExp, handler: MockRoute["handler"]) => {
      routes[method].push({ method, url, handler });
    };
  }

  function reset() {
    routes.get = [];
    routes.post = [];
    routes.put = [];
    routes.delete = [];
    routes.patch = [];
    calls.length = 0;
  }

  const instance = {
    get: makeMethod("get"),
    post: makeMethod("post"),
    put: makeMethod("put"),
    delete: makeMethod("delete"),
    patch: makeMethod("patch"),
    mockGet: register("get"),
    mockPost: register("post"),
    mockPut: register("put"),
    mockDelete: register("delete"),
    reset,
    calls,
  };

  return instance;
}

// 默认导出一个共享实例（用例间通过 reset 清理）
export const mockAxiosInstance = createMockAxiosInstance();

// 类型断言：供测试中 as AxiosInstance 使用
export function asAxiosInstance(mock: MockAxiosInstance): AxiosInstance {
  return mock as unknown as AxiosInstance;
}
