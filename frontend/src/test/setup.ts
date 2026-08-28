// Vitest 全局 setup：每个测试文件执行前运行一次。
// 注入 jest-dom 匹配器、jsdom 缺失的 API、全局 mock。
// 详见 v1.1.1 改进任务清单 T1。

import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, vi } from "vitest";
import { cleanup } from "@testing-library/react";

// 每个用例后清理 DOM，避免跨用例污染
afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.clearAllMocks();
  vi.resetModules();
});

// jsdom 缺失 matchMedia（部分组件布局依赖），补一个空实现
beforeEach(() => {
  if (!window.matchMedia) {
    Object.defineProperty(window, "matchMedia", {
      writable: true,
      value: (query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }),
    });
  }

  // IntersectionObserver 在 jsdom 中缺失，组件内若有 lazy 渲染会用到
  if (!("IntersectionObserver" in window)) {
    class MockIntersectionObserver {
      observe = vi.fn();
      unobserve = vi.fn();
      disconnect = vi.fn();
      takeRecords = vi.fn(() => []);
      root = null;
      rootMargin = "";
      thresholds = [];
    }
    (window as unknown as { IntersectionObserver: typeof MockIntersectionObserver }).IntersectionObserver =
      MockIntersectionObserver;
    (global as unknown as { IntersectionObserver: typeof MockIntersectionObserver }).IntersectionObserver =
      MockIntersectionObserver;
  }

  // ResizeObserver 同样缺失
  if (!("ResizeObserver" in window)) {
    class MockResizeObserver {
      observe = vi.fn();
      unobserve = vi.fn();
      disconnect = vi.fn();
    }
    (window as unknown as { ResizeObserver: typeof MockResizeObserver }).ResizeObserver =
      MockResizeObserver;
    (global as unknown as { ResizeObserver: typeof MockResizeObserver }).ResizeObserver =
      MockResizeObserver;
  }

  // scrollTo 在 jsdom 中是空函数但有些组件会调用，确保不抛错
  if (!window.scrollTo) {
    window.scrollTo = vi.fn();
  }

  // Radix UI 全家桶在 jsdom 下需要的 API polyfill
  // pointer events
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = vi.fn(() => false);
  }
  if (!Element.prototype.setPointerCapture) {
    Element.prototype.setPointerCapture = vi.fn(() => true);
  }
  if (!Element.prototype.releasePointerCapture) {
    Element.prototype.releasePointerCapture = vi.fn(() => true);
  }
  // scrollIntoView
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = vi.fn();
  }
  // focus options
  if (!HTMLElement.prototype.focus) {
    HTMLElement.prototype.focus = vi.fn();
  }
  // setStyle / getClientRects fallback
  if (!Element.prototype.scrollTo) {
    Element.prototype.scrollTo = vi.fn();
  }
});


