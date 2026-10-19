import * as React from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import * as z from "zod";
import { useForm } from "react-hook-form";

import { axios, axiosInstance } from "@/lib/axios";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Info, QrCode, KeyRound } from "lucide-react";
import { toast } from "sonner";
import { QrCodeLogin } from "./QrCodeLogin";

// 表单验证规则
export const accountFormSchema = z.object({
  accountType: z.string().min(1, "账户类型不能为空"),
  name: z.string().min(1, "账户名称不能为空"),
  // 115 类型字段
  cookie: z.string().optional(),
  // openlist 类型字段
  account: z.string().optional(),
  password: z.string().optional(),
  url: z.string().optional(),
}).refine((data) => {
  if (data.accountType === "115") {
    return data.cookie && data.cookie.length > 0;
  } else if (data.accountType === "openlist") {
    return data.account && data.password && data.url;
  }
  return true;
}, {
  message: "请填写对应账户类型的必需字段",
  path: ["accountType"]
});

export type AccountFormValues = z.infer<typeof accountFormSchema>;

interface AddAccountDialogProps {
  account?: AccountFormValues; // 编辑模式
  trigger?: React.ReactNode;   // 自定义按钮
  onSuccess?: () => void;      // 新增/编辑成功后的回调
}

export function AddAccountDialog({ account, trigger, onSuccess }: AddAccountDialogProps) {
  const [open, setOpen] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  // 115 Cookie 获取方式：qrcode=扫码登录，manual=手动粘贴
  const [cookieMode, setCookieMode] = React.useState<"qrcode" | "manual">("qrcode");

  const form = useForm<AccountFormValues>({
    resolver: zodResolver(accountFormSchema),
    defaultValues: account ?? {
      accountType: "",
      name: "",
      cookie: "",
      account: "",
      password: "",
      url: "",
    },
  });

  const watchAccountType = form.watch("accountType");

  const onSubmit = async (values: AccountFormValues) => {
    setLoading(true);
    try {
      console.log("提交的表单数据:", values);
      let response;
      if (account) {
        // 编辑 → PUT，传递 originalName 用于后端查找
        const payload = { ...values, originalName: account.name };
        response = await axiosInstance.put("/api/account", payload);
        const cascade = (
          response.data as
            | {
                cascade?: {
                  tasksUpdated?: number;
                  monitorConfigUpdated?: boolean;
                  dbRecordsRenamed?: Record<string, number>;
                };
              }
            | undefined
        )?.cascade;
        if (cascade) {
          const synced: string[] = [];
          if (cascade.tasksUpdated) synced.push(`${cascade.tasksUpdated} 个任务`);
          if (cascade.monitorConfigUpdated) synced.push("生活监控配置");
          if (cascade.dbRecordsRenamed) synced.push("索引库记录");
          toast.success(
            synced.length > 0
              ? `账号已改名，已同步 ${synced.join("、")}；旧 STRM 请到清理页扫描后重建`
              : "账号已更新"
          );
        } else {
          toast("账号更新成功");
        }
      } else {
        // 新增 → POST
        response = await axiosInstance.post("/api/account", values);
        console.log("保存成功响应:", response.data);
        toast("账号添加成功");
      }

      // 调用回调刷新列表
      onSuccess?.();

      setOpen(false);
      form.reset();
      setCookieMode("qrcode");
    } catch (err: unknown) {
      console.error("提交失败详情:", err);
      let errorMsg = "操作失败";
      if (axios.isAxiosError(err)) {
        const data = err.response?.data as
          | { error?: string; code?: string; existingName?: string }
          | undefined;
        // 同一 115 账号（UID 相同）已存在：引导直接更新已有账户的 Cookie。
        // 若用新名字再建一份，旧账户名下已生成的 STRM 会全部失效。
        if (data?.code === "duplicate_uid" && data.existingName) {
          const cookie = form.getValues("cookie") || "";
          const confirmed = window.confirm(
            `该 115 账号已存在（账户名：${data.existingName}）。\n\n` +
              `为避免已生成的 STRM 失效，请直接更新已有账户的 Cookie。\n` +
              `是否立即更新「${data.existingName}」的登录状态？`
          );
          if (confirmed && cookie) {
            try {
              await axiosInstance.put("/api/account", {
                name: data.existingName,
                originalName: data.existingName,
                accountType: "115",
                cookie,
              });
              toast.success(`已更新账户「${data.existingName}」的 Cookie`);
              onSuccess?.();
              setOpen(false);
              form.reset();
              setCookieMode("qrcode");
              return;
            } catch (updateErr) {
              const msg = axios.isAxiosError(updateErr)
                ? (updateErr.response?.data as { error?: string } | undefined)?.error ||
                  updateErr.message
                : "更新失败";
              toast.error(msg);
              return;
            }
          }
        }
        errorMsg = data?.error || err.message || errorMsg;
      } else if (err instanceof Error) {
        errorMsg = err.message || errorMsg;
      }
      toast.error(errorMsg);
    } finally {
      setLoading(false);
    }
  };

  // 扫码登录成功回调：回填 Cookie 并自动提交表单
  const handleQrcodeSuccess = React.useCallback((cookie: string) => {
    console.log("扫码成功，获取到 cookie:", cookie ? cookie.substring(0, 50) + "..." : "(空)");
    
    if (!cookie) {
      toast.error("获取的 Cookie 为空，请重试");
      return;
    }
    
    // 使用 form.setValue 同步设置所有必要字段
    form.setValue("cookie", cookie, { shouldValidate: false });
    form.setValue("accountType", "115", { shouldValidate: false });
    
    // 获取当前表单值
    const currentValues = form.getValues();
    console.log("当前表单值:", currentValues);

    // 账户名会写入 STRM 播放地址：不再自动生成「115账户-时间戳」名字。
    // 历史默认名是 STRM 大面积失效的根因之一（改名/重登后名字漂移）。
    if (!currentValues.name.trim()) {
      toast.success("扫码登录成功，请填写账户名称后点击「保存」");
      return;
    }

    toast.success("扫码登录成功，正在保存...");

    // 延迟 500ms 让状态更新后再提交表单
    setTimeout(() => {
      // 再次获取最新的表单值
      const values = form.getValues();
      console.log("提交前的表单值:", values);
      
      // 最终验证
      if (!values.accountType || !values.cookie) {
        console.error("表单数据不完整:", values);
        toast.error("表单数据不完整，请重试");
        return;
      }
      
      // 触发表单提交
      const submit = form.handleSubmit(onSubmit, (errors) => {
        console.error("表单验证失败:", errors);
        toast.error("表单验证失败，请检查表单填写是否正确");
      });
      submit();
    }, 500);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [form]);

  // 模拟可选账号类型
  const accountTypes = ["115", "openlist"];

  return (
    <Dialog open={open} onOpenChange={(v) => {
      setOpen(v);
      if (!v) {
        form.reset();
        setCookieMode("qrcode");
      }
    }}>
      <DialogTrigger asChild>
        {trigger ?? (
          <Button variant="outline">{account ? "Edit Account" : "Add Account"}</Button>
        )}
      </DialogTrigger>

      <DialogContent className="max-w-[95vw] sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>{account ? "编辑账户" : "新增账户"}</DialogTitle>
          <DialogDescription>
            {account ? "编辑你的账户信息" : "添加一个新的网盘账户"}
          </DialogDescription>
        </DialogHeader>

        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
            {/* 账号类型 */}
            <FormField
              control={form.control}
              name="accountType"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>账户类型</FormLabel>
                  <FormControl>
                    <Select onValueChange={field.onChange} value={field.value}>
                      <SelectTrigger>
                        <SelectValue placeholder="选择账户类型" />
                      </SelectTrigger>
                      <SelectContent>
                        {accountTypes.map((type) => (
                          <SelectItem key={type} value={type}>
                            {type}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            {/* 账户名称 */}
            <FormField
              control={form.control}
              name="name"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>账户名称</FormLabel>
                  <FormControl>
                    <Input placeholder="输入账户名称" {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            {/* 115 类型字段 */}
            {watchAccountType === "115" && (
              <>
                {/* Cookie 获取方式切换 */}
                <div className="space-y-2">
                  <FormLabel>Cookie 获取方式</FormLabel>
                  <div className="flex gap-2">
                    <Button
                      type="button"
                      variant={cookieMode === "qrcode" ? "default" : "outline"}
                      size="sm"
                      onClick={() => setCookieMode("qrcode")}
                      className="flex-1"
                    >
                      <QrCode className="w-4 h-4 mr-1.5" />
                      扫码登录
                    </Button>
                    <Button
                      type="button"
                      variant={cookieMode === "manual" ? "default" : "outline"}
                      size="sm"
                      onClick={() => setCookieMode("manual")}
                      className="flex-1"
                    >
                      <KeyRound className="w-4 h-4 mr-1.5" />
                      手动粘贴
                    </Button>
                  </div>
                </div>

                {/* 扫码登录模式 */}
                {cookieMode === "qrcode" ? (
                  <QrCodeLogin onSuccess={handleQrcodeSuccess} autoStart={true} />
                ) : (
                  /* 手动粘贴模式 */
                  <FormField
                    control={form.control}
                    name="cookie"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>Cookie</FormLabel>
                        <FormControl>
                          <Input placeholder="输入 115 网盘的 Cookie" {...field} />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                )}

                {/* 扫码成功后显示已填入的 Cookie（只读） */}
                {cookieMode === "qrcode" && form.watch("cookie") && (
                  <FormField
                    control={form.control}
                    name="cookie"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>Cookie（已通过扫码获取）</FormLabel>
                        <FormControl>
                          <Input
                            readOnly
                            value={field.value ? `${field.value.slice(0, 40)}...（长度 ${field.value.length}）` : ""}
                            className="bg-green-50 text-green-700"
                          />
                        </FormControl>
                      </FormItem>
                    )}
                  />
                )}

                <Alert>
                  <Info className="h-4 w-4" />
                  <AlertDescription className="space-y-1">
                    <p>
                      如果希望使用 302 重定向功能，推荐将账户名称与 openlist、cd2 的 115 账户用户名保持一致，这样可以保证正确回源。
                    </p>
                    <p className="text-muted-foreground">
                      账户名会写入已生成的 STRM 播放地址：改名会自动同步任务、生活监控与索引库，
                      但旧 STRM 内容不会被重写，需到「STRM 清理」页扫描并重建。
                    </p>
                  </AlertDescription>
                </Alert>
              </>
            )}

            {/* openlist 类型字段 */}
            {watchAccountType === "openlist" && (
              <>
                <FormField
                  control={form.control}
                  name="account"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>用户名</FormLabel>
                      <FormControl>
                        <Input placeholder="输入用户名" {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="password"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>密码</FormLabel>
                      <FormControl>
                        <Input type="password" placeholder="输入密码" {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="url"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>服务器地址</FormLabel>
                      <FormControl>
                        <Input placeholder="输入服务器地址，如: https://example.com" {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </>
            )}

            <DialogFooter>
              <DialogClose asChild>
                <Button variant="outline" disabled={loading}>
                  取消
                </Button>
              </DialogClose>
              <Button type="submit" disabled={loading}>
                {loading ? "保存中..." : "保存"}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
