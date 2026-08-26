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
    if (error.response?.status === 401) {
      clearToken();
      clearUsername();
      window.location.href = '/login';
    }
    return Promise.reject(error);
  }
);

export default axiosInstance;
