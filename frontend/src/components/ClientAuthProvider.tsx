import { useEffect, useState } from 'react';
import { useNavigate, useLocation } from 'react-router-dom';

interface ClientAuthProviderProps {
  children: React.ReactNode;
}

export default function ClientAuthProvider({ children }: ClientAuthProviderProps) {
  const [isAuthenticated, setIsAuthenticated] = useState<boolean | null>(null);
  const navigate = useNavigate();
  const location = useLocation();
  const pathname = location.pathname;

  useEffect(() => {
    // 检查是否是登录页面或公共页面
    const publicPaths = ['/login'];
    const isPublicPath = publicPaths.some(path => pathname.startsWith(path));
    
    if (isPublicPath) {
      setIsAuthenticated(true);
      return;
    }

    // 检查localStorage中的token
    const token = localStorage.getItem('auth-token');
    
    if (!token) {
      navigate('/login');
      return;
    }

    // 简单验证token格式（JWT应该有3个部分）
    const tokenParts = token.split('.');
    if (tokenParts.length !== 3) {
      localStorage.removeItem('auth-token');
      navigate('/login');
      return;
    }

    // 检查token是否过期
    try {
      // JWT 使用 URL-safe base64，需要转换为标准 base64
      const base64 = tokenParts[1].replace(/-/g, '+').replace(/_/g, '/');
      const padded = base64 + '='.repeat((4 - base64.length % 4) % 4);
      const payload = JSON.parse(atob(padded));
      const now = Math.floor(Date.now() / 1000);

      if (payload.exp && payload.exp < now) {
        localStorage.removeItem('auth-token');
        navigate('/login');
        return;
      }

      setIsAuthenticated(true);
    } catch {
      localStorage.removeItem('auth-token');
      navigate('/login');
    }
  }, [pathname, navigate]);

  // 显示加载状态，直到认证检查完成
  if (isAuthenticated === null) {
    return (
      <div className="flex items-center justify-center min-h-screen">
        <div className="text-center">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-gray-900 mx-auto"></div>
          <p className="mt-2 text-gray-600">验证登录状态...</p>
        </div>
      </div>
    );
  }

  return <>{children}</>;
}
