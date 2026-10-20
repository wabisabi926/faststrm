import axios from 'axios';

// 供 catch (e) 块内调用 axios.isAxiosError(e) 做类型守卫
export { axios };

const API_BASE_URL = import.meta.env.VITE_API_URL || '';

function getToken(): string | null {
  return localStorage.getItem('auth-token');
}

export function setToken(token: string): void {
  localStorage.setItem('auth-token', token);
}

export function clearToken(): void {
  localStorage.removeItem('auth-token');
}

export function setUsername(username: string): void {
  localStorage.setItem('auth-username', username);
}

export function getUsername(): string | null {
  return localStorage.getItem('auth-username');
}

export function clearUsername(): void {
  localStorage.removeItem('auth-username');
}

const MUST_CHANGE_PASSWORD_KEY = 'must-change-password';

export function setMustChangePassword(required: boolean): void {
  if (required) {
    localStorage.setItem(MUST_CHANGE_PASSWORD_KEY, '1');
  } else {
    localStorage.removeItem(MUST_CHANGE_PASSWORD_KEY);
  }
}

export function getMustChangePassword(): boolean {
  return localStorage.getItem(MUST_CHANGE_PASSWORD_KEY) === '1';
}

export const axiosInstance = axios.create({
  baseURL: API_BASE_URL,
  timeout: 30000,
});

axiosInstance.interceptors.request.use(
  (config) => {
    const token = getToken();
    if (token) {
      config.headers.Authorization = `Bearer ${token}`;
    }
    return config;
  },
  (error) => {
    return Promise.reject(error);
  }
);

axiosInstance.interceptors.response.use(
  (response) => response,
  (error) => {
    const url = String(error.config?.url || '');
    const code = error.response?.data?.code;
    if (error.response?.status === 403 && code === 'PASSWORD_CHANGE_REQUIRED') {
      setMustChangePassword(true);
      window.dispatchEvent(new Event('auth:password-change-required'));
      return Promise.reject(error);
    }
    // 登录/改密接口的 401 由页面自行展示，不能把用户踢回登录页
    if (error.response?.status === 401 && !url.includes('/api/auth/')) {
      clearToken();
      clearUsername();
      window.location.href = '/login';
    }
    return Promise.reject(error);
  }
);

export default axiosInstance;
