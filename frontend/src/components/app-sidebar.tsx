import {
  LayoutDashboard,
  Users,
  ListChecks,
  Trash2,
  Activity,
  BellRing,
  History,
  Settings,
  ShieldCheck,
  Github,
} from "lucide-react";

import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarSeparator,
} from "@/components/ui/sidebar";
import { Link, useLocation } from "react-router-dom";
import frontendPkg from "../../package.json";
type PackageJson = { version?: string } & Record<string, unknown>;
const appVersion = (frontendPkg as PackageJson).version ?? "";

type NavItem = {
  title: string;
  url: string;
  icon: typeof LayoutDashboard;
};

const navGroups: Array<{ label?: string; items: NavItem[] }> = [
  {
    items: [{ title: "概览", url: "/", icon: LayoutDashboard }],
  },
  {
    label: "媒体库",
    items: [
      { title: "账号管理", url: "/account", icon: Users },
      { title: "任务管理", url: "/task", icon: ListChecks },
      { title: "STRM 清理", url: "/cleanup", icon: Trash2 },
    ],
  },
  {
    label: "日志",
    items: [
      { title: "生活事件", url: "/life-events", icon: Activity },
      { title: "执行历史", url: "/history", icon: History },
    ],
  },
  {
    label: "设置",
    items: [
      { title: "通知中心", url: "/notify", icon: BellRing },
      { title: "设置中心", url: "/settings", icon: Settings },
      { title: "账号安全", url: "/account-security", icon: ShieldCheck },
    ],
  },
];

function isNavActive(url: string, pathname: string): boolean {
  if (url === "/") return pathname === "/";
  // 日志详情从执行历史打开，归属于执行历史
  if (url === "/history") return pathname === "/history" || pathname.startsWith("/log/");
  return pathname === url || pathname.startsWith(url + "/");
}

export function AppSidebar() {
  const location = useLocation();
  const pathname = location.pathname;
  return (
    <Sidebar collapsible="icon">
      <SidebarContent>
        <div className="p-4 border-b border-border/50">
          <Link to="/" className="flex items-center gap-3 group">
            <img
              src="/logo.png"
              alt="Fast Strm Logo"
              width={36}
              height={36}
              className="flex-shrink-0"
            />
            <div className="flex flex-col">
              <span className="text-lg font-bold text-foreground">Fast Strm</span>
              <span className="text-xs text-muted-foreground">更快、更强、更硬</span>
            </div>
          </Link>
        </div>
        {navGroups.map((group, index) => (
          <SidebarGroup key={group.label ?? `group-${index}`}>
            {group.label && <SidebarGroupLabel>{group.label}</SidebarGroupLabel>}
            <SidebarGroupContent>
              <SidebarMenu>
                {group.items.map((item) => {
                  const isActive = isNavActive(item.url, pathname);
                  return (
                    <SidebarMenuItem key={item.title}>
                      <SidebarMenuButton
                        asChild
                        tooltip={item.title}
                        isActive={isActive}
                      >
                        <Link to={item.url}>
                          <item.icon className="h-4 w-4" />
                          <span>{item.title}</span>
                        </Link>
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  );
                })}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        ))}
      </SidebarContent>
      <SidebarSeparator className="ml-0 mr-2 w-auto group-data-[collapsible=icon]:mx-0" />
      <SidebarFooter>
        <div className="flex items-center justify-between mx-2 mb-1 rounded-md px-2 py-1 text-xs group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0">
          <a
            href="https://github.com/wabisabi926/faststrm"
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-muted-foreground hover:text-foreground transition-colors group-data-[collapsible=icon]:justify-center"
          >
            <Github className="h-4 w-4" />
            <span className="group-data-[collapsible=icon]:hidden">GitHub</span>
          </a>
          <span className="px-2 py-0.5 rounded bg-muted text-foreground/80 group-data-[collapsible=icon]:hidden">v{appVersion}</span>
        </div>
      </SidebarFooter>
    </Sidebar>
  );
}
