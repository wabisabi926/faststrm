import { ListChecks, Inbox, Settings, Github, Bot, History, Activity, Bell, ShieldAlert } from "lucide-react";

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

const items = [
  {
    title: "账户",
    url: "/account",
    icon: Inbox,
  },
  {
    title: "任务",
    url: "/task",
    icon: ListChecks,
  },
  {
    title: "设置",
    url: "/settings",
    icon: Settings,
  },
];

const notifyItems = [
  {
    title: "账户状态",
    url: "/account-alerts",
    icon: ShieldAlert,
  },
  {
    title: "TG 通知",
    url: "/tg-notify",
    icon: Bot,
  },
  {
    title: "Emby 通知",
    url: "/emby-notify",
    icon: Bell,
  },
];

const logItems = [
  {
    title: "任务历史",
    url: "/history",
    icon: History,
  },
  {
    title: "生活事件",
    url: "/life-events",
    icon: Activity,
  },
];

export function AppSidebar() {
  const location = useLocation();
  const pathname = location.pathname;
  return (
    <Sidebar collapsible="icon">
      <SidebarContent>
        <div className="p-4 border-b border-border/50">
          <Link to="/task" className="flex items-center gap-3 group">
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
        <SidebarGroup>
          <SidebarGroupLabel>主菜单</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {items.map((item) => {
                const isActive = pathname === item.url;
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
        
        <SidebarGroup>
          <SidebarGroupLabel>通知</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {notifyItems.map((item) => {
                const isActive = pathname === item.url;
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

        <SidebarGroup>
          <SidebarGroupLabel>日志</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {logItems.map((item) => {
                const isActive = pathname === item.url;
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
