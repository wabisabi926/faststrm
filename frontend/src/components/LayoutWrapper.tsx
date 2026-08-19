import { useLocation, useNavigate } from "react-router-dom";
import { SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar";
import { AppSidebar } from "@/components/app-sidebar";
import { Settings, BookOpen, LogOut, User, Sun, Moon } from "lucide-react";
import axiosInstance, { clearToken, clearUsername, getUsername } from "@/lib/axios";
import {
  Menubar,
  MenubarContent,
  MenubarItem,
  MenubarMenu,
  MenubarTrigger,
  MenubarSeparator,
} from "@/components/ui/menubar";
import { useState, useEffect } from "react";

export default function LayoutWrapper({ children }: { children: React.ReactNode }) {
  const location = useLocation();
  const pathname = location.pathname;
  const navigate = useNavigate();
  const [username, setUsername] = useState("");
  const [isDark, setIsDark] = useState(false);

  useEffect(() => {
    const savedUsername = getUsername();
    if (savedUsername) {
      setUsername(savedUsername);
    }
    const savedTheme = localStorage.getItem("theme");
    if (savedTheme === "dark") {
      document.documentElement.classList.add("dark");
      document.documentElement.classList.remove("light");
      setIsDark(true);
    } else {
      document.documentElement.classList.remove("dark");
      document.documentElement.classList.add("light");
      setIsDark(false);
    }
  }, []);

  if (pathname === "/login") {
    return <>{children}</>;
  }

  const logout = async () => {
    try {
      await axiosInstance.post("/api/auth/logout");
    } catch {
    }
    clearToken();
    clearUsername();
    navigate("/login");
  };

  const toggleTheme = () => {
    const newDark = !isDark;
    setIsDark(newDark);
    if (newDark) {
      document.documentElement.classList.add("dark");
      document.documentElement.classList.remove("light");
      localStorage.setItem("theme", "dark");
    } else {
      document.documentElement.classList.remove("dark");
      document.documentElement.classList.add("light");
      localStorage.setItem("theme", "light");
    }
  };

  return (
    <>
      <SidebarProvider>
          <AppSidebar />
          <div className="flex flex-col w-full min-h-screen pl-0">
            <header className="w-full border-b border-border flex items-center gap-2 px-3 py-2 flex-nowrap bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60">
              <SidebarTrigger />
              <div className="ml-auto flex items-center gap-1">
                <button
                  onClick={toggleTheme}
                  className="inline-flex items-center justify-center rounded-md p-2 hover:bg-accent hover:text-accent-foreground transition-colors"
                  aria-label="切换主题"
                  title={isDark ? "切换到浅色模式" : "切换到深色模式"}
                >
                  {isDark ? <Sun className="h-5 w-5" /> : <Moon className="h-5 w-5" />}
                </button>
                <a
                  href="https://github.com/wabisabi926/faststrm/wiki"
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center justify-center rounded-md p-2 hover:bg-accent hover:text-accent-foreground transition-colors"
                  aria-label="Wiki"
                  title="文档"
                >
                  <BookOpen className="h-5 w-5" />
                </a>
                <Menubar className="border-0 shadow-none">
                  <MenubarMenu>
                    <MenubarTrigger className="gap-1 px-2 py-1.5 cursor-pointer hover:bg-accent rounded-md">
                      <User className="h-4 w-4" />
                      <span className="text-sm font-medium">{username || "用户"}</span>
                    </MenubarTrigger>
                    <MenubarContent align="end" className="min-w-[160px]">
                      <div className="px-2 py-1.5 text-sm text-muted-foreground">
                        已登录为 <span className="font-medium text-foreground">{username || "用户"}</span>
                      </div>
                      <MenubarSeparator />
                      <MenubarItem onClick={() => logout()} className="text-red-600 dark:text-red-400 cursor-pointer">
                        <LogOut className="mr-2 h-4 w-4" />
                        退出登录
                      </MenubarItem>
                    </MenubarContent>
                  </MenubarMenu>
                </Menubar>
              </div>
            </header>
            <main className="flex-1 p-3 sm:p-4 md:p-6 bg-background min-w-0 overflow-x-hidden">
              {children}
            </main>
          </div>
      </SidebarProvider>
    </>
  );
}
