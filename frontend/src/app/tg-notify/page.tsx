"use client";

import { useState, useEffect } from "react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Separator } from "@/components/ui/separator";
import { Bot, Settings, MessageSquare, CheckCircle, XCircle, AlertCircle, RefreshCw, Play, Square, Users, Plus, Trash2, UserPlus } from "lucide-react";
import { Checkbox } from "@/components/ui/checkbox";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import axiosInstance from "@/lib/axios";

interface TelegramConfig {
  botToken?: string;
  chatId?: string;
  webhookUrl?: string;
  enabled?: boolean;
}

interface BotInfo {
  id: number;
  is_bot: boolean;
  first_name: string;
  username: string;
  can_join_groups: boolean;
  can_read_all_group_messages: boolean;
  supports_inline_queries: boolean;
}

interface WebhookInfo {
  url: string;
  has_custom_certificate: boolean;
  pending_update_count: number;
  last_error_date?: number;
  last_error_message?: string;
  max_connections?: number;
  allowed_updates?: string[];
}

interface TelegramUser {
  id: number;
}

export default function TelegramNotifyPage() {
  const [activeTab, setActiveTab] = useState<"bot" | "users">("bot");

  // Bot 配置相关状态
  const [config, setConfig] = useState<TelegramConfig>({});
  const [botInfo, setBotInfo] = useState<BotInfo | null>(null);
  const [webhookInfo, setWebhookInfo] = useState<WebhookInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [pollingStatus, setPollingStatus] = useState<{ polling: boolean; message: string } | null>(null);

  // 用户管理相关状态
  const [users, setUsers] = useState<TelegramUser[]>([]);
  const [newUserId, setNewUserId] = useState("");
  const [addDialogOpen, setAddDialogOpen] = useState(false);
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [userToDelete, setUserToDelete] = useState<number | null>(null);

  // 加载数据
  useEffect(() => {
    loadBotInfo();
    checkPollingStatus();
    if (activeTab === "users") {
      loadUsers();
    }
  }, [activeTab]);

  const loadBotInfo = async () => {
    try {
      setLoading(true);
      const response = await axiosInstance.get("/api/notify/bot");
      if (response.data.configured) {
        setBotInfo(response.data.bot.result);
        setWebhookInfo(response.data.webhook.result);
        setConfig({
          botToken: response.data.botToken || "",
          chatId: response.data.chatId || "",
          webhookUrl: response.data.webhook.result?.url || "",
          enabled: response.data.enabled !== false,
        });
      }
    } catch (error) {
      console.error("加载 Bot 信息失败:", error);
    } finally {
      setLoading(false);
    }
  };

  const handleSave = async () => {
    try {
      setLoading(true);
      setError(null);
      setSuccess(null);

      const response = await axiosInstance.post("/api/notify/bot", {
        botToken: config.botToken,
        chatId: config.chatId,
        webhookUrl: config.webhookUrl,
        enabled: config.enabled !== false,
      });

      if (response.data.success) {
        setSuccess("Telegram 机器人配置成功！");
        setBotInfo(response.data.bot);
        setConfig({
          botToken: response.data.botToken || "",
          chatId: response.data.chatId || "",
          webhookUrl: response.data.webhook?.result?.url || "",
        });
        await loadBotInfo();
      }
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string; details?: string } }; message?: string };
      const errorMessage = axiosError.response?.data?.error || axiosError.message || "配置失败";
      const errorDetails = axiosError.response?.data?.details || "";
      setError(errorDetails ? `${errorMessage}: ${errorDetails}` : errorMessage);
    } finally {
      setLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!confirm("确定要删除 Telegram 机器人配置吗？")) {
      return;
    }

    try {
      setLoading(true);
      setError(null);
      setSuccess(null);

      await axiosInstance.delete("/api/notify/bot");
      setSuccess("Telegram 机器人配置已删除！");
      setBotInfo(null);
      setWebhookInfo(null);
      setConfig({});
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "删除配置失败");
    } finally {
      setLoading(false);
    }
  };

  const checkPollingStatus = async () => {
    try {
      const response = await axiosInstance.get("/api/notify/polling");
      setPollingStatus(response.data);
    } catch (error) {
      console.error("检查轮询状态失败:", error);
    }
  };

  const startPolling = async () => {
    try {
      setLoading(true);
      setError(null);
      setSuccess(null);

      const response = await axiosInstance.post("/api/notify/polling");

      if (response.data.success) {
        setSuccess("轮询已启动！");
        await checkPollingStatus();
      }
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "启动轮询失败");
    } finally {
      setLoading(false);
    }
  };

  const stopPolling = async () => {
    try {
      setLoading(true);
      setError(null);
      setSuccess(null);

      const response = await axiosInstance.delete("/api/notify/polling");

      if (response.data.success) {
        setSuccess("轮询已停止！");
        await checkPollingStatus();
      }
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "停止轮询失败");
    } finally {
      setLoading(false);
    }
  };

  const testBot = async () => {
    if (!config.chatId) {
      setError("请先设置 Chat ID");
      return;
    }

    try {
      setLoading(true);
      setError(null);
      setSuccess(null);

      await axiosInstance.post("/api/notify/send", {
        message: "🤖 Fast Strm 测试消息！",
        type: "info",
      });

      setSuccess("测试消息发送成功！");
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "测试消息发送失败");
    } finally {
      setLoading(false);
    }
  };

  // 用户管理相关函数
  const loadUsers = async () => {
    try {
      setLoading(true);
      const response = await axiosInstance.get("/api/notify/users");
      setUsers(response.data.users || []);
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "加载用户列表失败");
    } finally {
      setLoading(false);
    }
  };

  const handleAddUser = async () => {
    if (!newUserId.trim()) {
      setError("请输入用户 ID");
      return;
    }

    const userId = parseInt(newUserId);
    if (isNaN(userId)) {
      setError("请输入有效的用户 ID");
      return;
    }

    try {
      setLoading(true);
      setError(null);
      setSuccess(null);

      const response = await axiosInstance.post("/api/notify/users", {
        userId: userId,
      });

      if (response.data.success) {
        setSuccess("用户添加成功！");
        setNewUserId("");
        setAddDialogOpen(false);
        await loadUsers();
      }
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "添加用户失败");
    } finally {
      setLoading(false);
    }
  };

  const handleDeleteUser = async () => {
    if (!userToDelete) return;

    try {
      setLoading(true);
      setError(null);
      setSuccess(null);

      await axiosInstance.delete(`/api/notify/users?userId=${userToDelete}`);

      setSuccess("用户已删除！");
      setDeleteDialogOpen(false);
      setUserToDelete(null);
      await loadUsers();
    } catch (error) {
      const axiosError = error as { response?: { data?: { error?: string } } };
      setError(axiosError.response?.data?.error || "删除用户失败");
    } finally {
      setLoading(false);
    }
  };

  const openDeleteDialog = (userId: number) => {
    setUserToDelete(userId);
    setDeleteDialogOpen(true);
  };

  const closeDeleteDialog = () => {
    setUserToDelete(null);
    setDeleteDialogOpen(false);
  };

  return (
    <div className="container mx-auto p-6 space-y-6">
      {/* 页面标题 */}
      <div className="flex items-center space-x-2">
        <Bot className="h-6 w-6" />
        <h1 className="text-3xl font-bold">Telegram 通知</h1>
      </div>

      {/* Tab 切换 */}
      <div className="flex gap-1 border-b border-border">
        <button
          onClick={() => setActiveTab("bot")}
          className={`px-4 py-2 text-sm font-medium transition-colors relative ${
            activeTab === "bot"
              ? "text-foreground"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          <Settings className="inline-block h-4 w-4 mr-1" />
          机器人配置
          {activeTab === "bot" && (
            <span className="absolute bottom-0 left-0 right-0 h-0.5 bg-foreground" />
          )}
        </button>
        <button
          onClick={() => setActiveTab("users")}
          className={`px-4 py-2 text-sm font-medium transition-colors relative ${
            activeTab === "users"
              ? "text-foreground"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          <Users className="inline-block h-4 w-4 mr-1" />
          用户管理
          {activeTab === "users" && (
            <span className="absolute bottom-0 left-0 right-0 h-0.5 bg-foreground" />
          )}
        </button>
      </div>

      {/* 错误/成功提示 */}
      {error && (
        <Alert variant="destructive">
          <XCircle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {success && (
        <Alert>
          <CheckCircle className="h-4 w-4" />
          <AlertDescription>{success}</AlertDescription>
        </Alert>
      )}

      {/* Bot 配置 Tab 内容 */}
      {activeTab === "bot" && (
        <div className="space-y-6">
          <div className="grid gap-6 md:grid-cols-2">
            {/* 机器人配置 */}
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center space-x-2">
                  <Settings className="h-5 w-5" />
                  <span>机器人配置</span>
                </CardTitle>
                <CardDescription>配置你的 Telegram 机器人</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="botToken">机器人 Token</Label>
                  <Input
                    id="botToken"
                    type="password"
                    placeholder="123456789:ABCdefGHIjklMNOpqrsTUVwxyz"
                    value={config.botToken || ""}
                    onChange={(e) => setConfig({ ...config, botToken: e.target.value })}
                  />
                  <p className="text-sm text-muted-foreground">
                    从 @BotFather 获取。格式：数字:35位字符
                  </p>
                </div>

                <div className="space-y-2">
                  <Label htmlFor="chatId">聊天 ID</Label>
                  <Input
                    id="chatId"
                    placeholder="输入你的聊天 ID"
                    value={config.chatId || ""}
                    onChange={(e) => setConfig({ ...config, chatId: e.target.value })}
                  />
                  <p className="text-sm text-muted-foreground">
                    向机器人发送消息并查看日志获取聊天 ID
                  </p>
                </div>

                <div className="space-y-2">
                  <Label htmlFor="webhookUrl">Webhook URL（可选）</Label>
                  <Input
                    id="webhookUrl"
                    placeholder="https://你的域名.com/api/notify/webhook"
                    value={config.webhookUrl || ""}
                    onChange={(e) => setConfig({ ...config, webhookUrl: e.target.value })}
                  />
                  <p className="text-sm text-muted-foreground">
                    留空则使用轮询模式
                  </p>
                </div>

                <div className="flex items-center space-x-2">
                  <Checkbox
                    id="enabled"
                    checked={config.enabled !== false}
                    onCheckedChange={(checked) => setConfig({ ...config, enabled: checked === true })}
                  />
                  <label
                    htmlFor="enabled"
                    className="text-sm font-medium leading-none cursor-pointer"
                  >
                    启用通知（任务开始/完成/失败自动推送）
                  </label>
                </div>

                <div className="flex space-x-2">
                  <Button onClick={handleSave} disabled={loading || !config.botToken}>
                    {loading ? "保存中..." : "保存配置"}
                  </Button>
                  {botInfo && (
                    <Button variant="outline" onClick={handleDelete} disabled={loading}>
                      删除配置
                    </Button>
                  )}
                </div>
              </CardContent>
            </Card>

            {/* 机器人状态 */}
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center space-x-2">
                  <Bot className="h-5 w-5" />
                  <span>机器人状态</span>
                </CardTitle>
                <CardDescription>当前机器人信息和状态</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                {botInfo ? (
                  <>
                    <div className="flex items-center space-x-2">
                      <Badge variant="outline" className="bg-green-50 text-green-700">
                        <CheckCircle className="h-3 w-3 mr-1" />
                        已连接
                      </Badge>
                    </div>

                    <div className="space-y-2">
                      <div className="flex justify-between">
                        <span className="text-sm font-medium">机器人名称：</span>
                        <span className="text-sm">{botInfo.first_name}</span>
                      </div>
                      <div className="flex justify-between">
                        <span className="text-sm font-medium">用户名：</span>
                        <span className="text-sm">@{botInfo.username}</span>
                      </div>
                      <div className="flex justify-between">
                        <span className="text-sm font-medium">机器人 ID：</span>
                        <span className="text-sm">{botInfo.id}</span>
                      </div>
                    </div>

                    <Separator />

                    <div className="space-y-2">
                      <h4 className="text-sm font-medium">功能权限：</h4>
                      <div className="space-y-1">
                        <div className="flex items-center space-x-2">
                          {botInfo.can_join_groups ? (
                            <CheckCircle className="h-3 w-3 text-green-500" />
                          ) : (
                            <XCircle className="h-3 w-3 text-red-500" />
                          )}
                          <span className="text-xs">可加入群组</span>
                        </div>
                        <div className="flex items-center space-x-2">
                          {botInfo.can_read_all_group_messages ? (
                            <CheckCircle className="h-3 w-3 text-green-500" />
                          ) : (
                            <XCircle className="h-3 w-3 text-red-500" />
                          )}
                          <span className="text-xs">可读取所有群组消息</span>
                        </div>
                        <div className="flex items-center space-x-2">
                          {botInfo.supports_inline_queries ? (
                            <CheckCircle className="h-3 w-3 text-green-500" />
                          ) : (
                            <XCircle className="h-3 w-3 text-red-500" />
                          )}
                          <span className="text-xs">支持内联查询</span>
                        </div>
                      </div>
                    </div>

                    <Button onClick={testBot} disabled={loading} className="w-full">
                      <MessageSquare className="h-4 w-4 mr-2" />
                      发送测试消息
                    </Button>
                  </>
                ) : (
                  <div className="text-center py-8">
                    <AlertCircle className="h-8 w-8 mx-auto text-muted-foreground mb-2" />
                    <p className="text-sm text-muted-foreground">
                      未配置机器人，请先配置。
                    </p>
                  </div>
                )}
              </CardContent>
            </Card>
          </div>

          {/* 轮询控制 */}
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center space-x-2">
                <RefreshCw className="h-5 w-5" />
                <span>轮询控制</span>
              </CardTitle>
              <CardDescription>控制机器人接收消息的模式</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              {pollingStatus && (
                <div className="flex items-center space-x-2">
                  <Badge variant={pollingStatus.polling ? "default" : "outline"}>
                    {pollingStatus.polling ? "轮询中" : "Webhook 模式"}
                  </Badge>
                  <span className="text-sm text-muted-foreground">{pollingStatus.message}</span>
                </div>
              )}

              <div className="flex space-x-2">
                <Button
                  onClick={startPolling}
                  disabled={loading || (pollingStatus?.polling === true)}
                  variant="outline"
                >
                  <Play className="h-4 w-4 mr-2" />
                  启动轮询
                </Button>
                <Button
                  onClick={stopPolling}
                  disabled={loading || (pollingStatus?.polling === false)}
                  variant="outline"
                >
                  <Square className="h-4 w-4 mr-2" />
                  停止轮询
                </Button>
                <Button
                  onClick={checkPollingStatus}
                  disabled={loading}
                  variant="outline"
                >
                  <RefreshCw className="h-4 w-4 mr-2" />
                  刷新状态
                </Button>
              </div>

              <div className="text-sm text-muted-foreground space-y-1">
                <p><strong>轮询模式：</strong>机器人每 5 秒检查新消息（降低频率以避免冲突）</p>
                <p><strong>Webhook 模式：</strong>Telegram 直接向服务器发送消息</p>
              </div>
            </CardContent>
          </Card>

          {/* Webhook 信息 */}
          {webhookInfo && (
            <Card>
              <CardHeader>
                <CardTitle>Webhook 信息</CardTitle>
                <CardDescription>当前 Webhook 配置和状态</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="space-y-2">
                  <div className="flex justify-between">
                    <span className="text-sm font-medium">Webhook URL：</span>
                    <span className="text-sm font-mono">{webhookInfo.url || "未设置"}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-sm font-medium">待处理更新：</span>
                    <Badge variant={webhookInfo.pending_update_count > 0 ? "destructive" : "outline"}>
                      {webhookInfo.pending_update_count}
                    </Badge>
                  </div>
                  {webhookInfo.last_error_message && (
                    <div className="space-y-1">
                      <span className="text-sm font-medium text-red-600">最后错误：</span>
                      <p className="text-sm text-red-600">{webhookInfo.last_error_message}</p>
                    </div>
                  )}
                </div>
              </CardContent>
            </Card>
          )}
        </div>
      )}

      {/* 用户管理 Tab 内容 */}
      {activeTab === "users" && (
        <div className="space-y-6">
          <Card>
            <CardHeader>
              <div className="flex items-center justify-between">
                <div>
                  <CardTitle className="flex items-center space-x-2">
                    <UserPlus className="h-5 w-5" />
                    <span>授权用户</span>
                  </CardTitle>
                  <CardDescription>管理可访问机器人的用户</CardDescription>
                </div>
                <Dialog open={addDialogOpen} onOpenChange={setAddDialogOpen}>
                  <DialogTrigger asChild>
                    <Button>
                      <Plus className="h-4 w-4 mr-2" />
                      添加用户
                    </Button>
                  </DialogTrigger>
                  <DialogContent>
                    <DialogHeader>
                      <DialogTitle>添加新用户</DialogTitle>
                      <DialogDescription>
                        将新用户添加到授权列表，该用户可以使用所有机器人命令。
                      </DialogDescription>
                    </DialogHeader>
                    <div className="space-y-4">
                      <div className="space-y-2">
                        <Label htmlFor="userId">用户 ID</Label>
                        <Input
                          id="userId"
                          type="number"
                          placeholder="输入 Telegram 用户 ID"
                          value={newUserId}
                          onChange={(e) => setNewUserId(e.target.value)}
                        />
                        <p className="text-sm text-muted-foreground">
                          向机器人发送消息并查看日志获取用户 ID
                        </p>
                      </div>
                    </div>
                    <DialogFooter>
                      <Button variant="outline" onClick={() => setAddDialogOpen(false)}>
                        取消
                      </Button>
                      <Button onClick={handleAddUser} disabled={loading}>
                        {loading ? "添加中..." : "添加"}
                      </Button>
                    </DialogFooter>
                  </DialogContent>
                </Dialog>
              </div>
            </CardHeader>
            <CardContent>
              {users.length === 0 ? (
                <div className="text-center py-8">
                  <AlertCircle className="h-8 w-8 mx-auto text-muted-foreground mb-2" />
                  <p className="text-sm text-muted-foreground mb-4">
                    暂无授权用户。
                  </p>
                  <Button onClick={() => setAddDialogOpen(true)}>
                    <Plus className="h-4 w-4 mr-2" />
                    添加第一个用户
                  </Button>
                </div>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>用户 ID</TableHead>
                      <TableHead>状态</TableHead>
                      <TableHead>操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {users.map((user) => (
                      <TableRow key={user.id}>
                        <TableCell className="font-mono">{user.id}</TableCell>
                        <TableCell>
                          <Badge variant="outline" className="bg-green-50 text-green-700">
                            <CheckCircle className="h-3 w-3 mr-1" />
                            已授权
                          </Badge>
                        </TableCell>
                        <TableCell>
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => openDeleteDialog(user.id)}
                            className="text-red-600 hover:text-red-700"
                          >
                            <Trash2 className="h-4 w-4 mr-1" />
                            移除
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>

          {/* 删除确认对话框 */}
          <Dialog open={deleteDialogOpen} onOpenChange={setDeleteDialogOpen}>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>删除用户</DialogTitle>
                <DialogDescription>
                  确定要从授权列表中移除用户 <code className="bg-muted px-1 rounded">{userToDelete}</code> 吗？
                  <br />
                  <br />
                  此操作无法撤销，该用户将无法使用机器人命令。
                </DialogDescription>
              </DialogHeader>
              <DialogFooter>
                <Button variant="outline" onClick={closeDeleteDialog}>
                  取消
                </Button>
                <Button variant="destructive" onClick={handleDeleteUser} disabled={loading}>
                  {loading ? "删除中..." : "删除"}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        </div>
      )}
    </div>
  );
}
