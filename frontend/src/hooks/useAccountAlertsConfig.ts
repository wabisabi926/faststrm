// 账号告警配置的共享逻辑：类型、默认值、读取与保存。
//
// 「网盘账号」页的健康面板与「通知中心 › 账号告警」页编辑的是同一份配置
// （`/api/notify/alerts`），此前两个页面各自复制了一份 interface / 默认值 /
// GET+POST 代码，容易出现默认值不一致或保存范围不同步。统一收敛到这里：
//   - 两页共用同一套类型与默认值；
//   - save() 只提交调用方显式给出的字段（后端为指针字段，按字段合并），
//     避免一页保存把另一页刚改的开关覆盖回旧值。
import { useCallback, useEffect, useRef, useState } from "react";
import axiosInstance from "@/lib/axios";

export interface CookieInspectConfig {
  enabled: boolean;
  intervalHours: number;
}

export interface AccountAlertsConfig {
  enabled: boolean;
  onError: boolean;
  onRecover: boolean;
  cookieInspect: CookieInspectConfig;
}

/** 账号告警配置的局部更新载荷：只提交自己负责的字段。 */
export type AccountAlertsPatch = Partial<Omit<AccountAlertsConfig, "cookieInspect">> & {
  cookieInspect?: Partial<CookieInspectConfig>;
};

export const DEFAULT_ACCOUNT_ALERTS_CONFIG: AccountAlertsConfig = {
  enabled: false,
  onError: true,
  onRecover: true,
  cookieInspect: { enabled: true, intervalHours: 6 },
};

export const COOKIE_INSPECT_MIN_HOURS = 1;
export const COOKIE_INSPECT_MAX_HOURS = 168;

/** 把巡检间隔收敛到合法区间，非法输入回退默认值。 */
export function clampInspectIntervalHours(value: number): number {
  if (!Number.isFinite(value)) return DEFAULT_ACCOUNT_ALERTS_CONFIG.cookieInspect.intervalHours;
  return Math.min(COOKIE_INSPECT_MAX_HOURS, Math.max(COOKIE_INSPECT_MIN_HOURS, Math.round(value)));
}

function normalizeConfig(raw: unknown): AccountAlertsConfig {
  const data = (raw ?? {}) as Partial<AccountAlertsConfig>;
  return {
    enabled: data.enabled ?? DEFAULT_ACCOUNT_ALERTS_CONFIG.enabled,
    onError: data.onError ?? DEFAULT_ACCOUNT_ALERTS_CONFIG.onError,
    onRecover: data.onRecover ?? DEFAULT_ACCOUNT_ALERTS_CONFIG.onRecover,
    cookieInspect: {
      enabled: data.cookieInspect?.enabled ?? DEFAULT_ACCOUNT_ALERTS_CONFIG.cookieInspect.enabled,
      intervalHours: clampInspectIntervalHours(
        data.cookieInspect?.intervalHours ??
          DEFAULT_ACCOUNT_ALERTS_CONFIG.cookieInspect.intervalHours
      ),
    },
  };
}

function extractError(err: unknown, fallback: string): string {
  const axiosErr = err as { response?: { data?: { error?: string } } };
  return axiosErr.response?.data?.error || fallback;
}

export interface UseAccountAlertsConfigResult {
  config: AccountAlertsConfig;
  /** 首次加载是否已完成（未完成时表单应禁用） */
  loaded: boolean;
  loading: boolean;
  saving: boolean;
  error: string;
  success: string;
  reload: () => Promise<void>;
  /** 仅提交 patch 中出现的字段；成功返回 true */
  save: (patch: AccountAlertsPatch, successMessage?: string) => Promise<boolean>;
  /** 本地修改（不落库），用于表单受控 */
  update: (patch: AccountAlertsPatch) => void;
  clearFeedback: () => void;
}

/**
 * 账号告警配置的读取/保存 hook。
 * 两个页面各自实例化，互不共享内存状态，但类型、默认值与接口语义一致。
 */
export function useAccountAlertsConfig(): UseAccountAlertsConfigResult {
  const [config, setConfig] = useState<AccountAlertsConfig>(DEFAULT_ACCOUNT_ALERTS_CONFIG);
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const aliveRef = useRef(true);

  useEffect(() => {
    aliveRef.current = true;
    return () => {
      aliveRef.current = false;
    };
  }, []);

  const reload = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await axiosInstance.get("/api/notify/alerts");
      if (!aliveRef.current) return;
      setConfig(normalizeConfig(response.data));
      setLoaded(true);
    } catch (err) {
      if (!aliveRef.current) return;
      setError(extractError(err, "加载账号告警配置失败"));
    } finally {
      if (aliveRef.current) setLoading(false);
    }
  }, []);

  const update = useCallback((patch: AccountAlertsPatch) => {
    setConfig((prev) => ({
      ...prev,
      ...patch,
      cookieInspect: { ...prev.cookieInspect, ...(patch.cookieInspect ?? {}) },
    }));
  }, []);

  const save = useCallback(
    async (patch: AccountAlertsPatch, successMessage = "账号告警设置保存成功") => {
      setSaving(true);
      setError("");
      setSuccess("");
      try {
        await axiosInstance.post("/api/notify/alerts", patch);
        if (aliveRef.current) {
          // 用刚提交的字段回填本地快照，避免界面显示与后端不一致
          update(patch);
          setSuccess(successMessage);
        }
        return true;
      } catch (err) {
        if (aliveRef.current) setError(extractError(err, "保存账号告警设置失败"));
        return false;
      } finally {
        if (aliveRef.current) setSaving(false);
      }
    },
    [update]
  );

  const clearFeedback = useCallback(() => {
    setError("");
    setSuccess("");
  }, []);

  return { config, loaded, loading, saving, error, success, reload, save, update, clearFeedback };
}
