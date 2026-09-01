import * as React from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import * as z from "zod";
import { useForm } from "react-hook-form";
import { Button } from "@/components/ui/button";
import axiosInstance from "@/lib/axios";
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
import { FolderOpen } from "lucide-react";
import { DirectoryTreeDialog } from "./DirectoryTreeDialog";
import { LocalDirectoryTreeDialog } from "./LocalDirectoryTreeDialog";
import { HelpLabel } from "./HelpLabel";
import { AdvancedStrmSettings } from "./AdvancedStrmSettings";
import { resolveStrmSettings } from "@/lib/strmUtils";

export const taskFormSchema = z.object({
  account: z.string().min(1, "账户不能为空"),
  originPath: z.string().min(1, "远程路径不能为空"),
  targetPath: z.string().optional(),
  strmType: z.string().optional(),
  strmPrefix: z.string().optional(),
  removeExtraFiles: z.boolean().optional(),
});

export type TaskFormValues = z.infer<typeof taskFormSchema>;

interface AddTaskDialogProps {
  task?: TaskFormValues & { name?: string; id?: string };
  trigger?: React.ReactNode;
  onSuccess?: () => void;
  accounts?: Array<{ name: string; accountType: string }>;
  accountsLoading?: boolean;
}
export function AddTaskDialog({
  task,
  trigger,
  onSuccess,
  accounts = [],
  accountsLoading = false,
}: AddTaskDialogProps) {
  const [open, setOpen] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  const [directoryDialogOpen, setDirectoryDialogOpen] = React.useState(false);
  const [localDirectoryDialogOpen, setLocalDirectoryDialogOpen] = React.useState(false);
  const [advancedOpen, setAdvancedOpen] = React.useState(false);
  const [formValues, setFormValues] = React.useState({
    strmPrefix: "",
    originPath: "",
    account: "",
  });

  // 获取选中账户的类型
  const getAccountType = (accountName: string) => {
    const selectedAccount = accounts.find((acc) => acc.name === accountName);
    return selectedAccount?.accountType || "";
  };

  // 检查当前选中的账户是否是 115 类型
  const is115Account = getAccountType(formValues.account) === "115";

  // 计算预览路径
  const getPreviewPath = () => {
    const { strmPrefix, originPath, account } = formValues;
    if (!originPath) {
      return "请输入远程路径";
    }
    if (!strmPrefix) {
      return `留空使用全局前缀 → .../${account || "{account}"}/${originPath}/....../abc.mkv`;
    }
    const resolved = resolveStrmSettings(account, {
      strmPrefix,
    });
    return `${resolved.strmPrefix}/${originPath}/....../abc.mkv`;
  };

  // 编辑时，去掉 strmPrefix 末尾可能残留的账号后缀（旧版数据兼容）
  const getInitialStrmPrefix = () => {
    if (!task) return "";
    let prefix = task.strmPrefix || "";
    // 旧版数据可能拼接了 /account 或 /api/strm，清理掉
    if (task.account && prefix.endsWith("/" + task.account)) {
      prefix = prefix.slice(0, -(task.account.length + 1));
    }
    if (prefix.endsWith("/api/strm")) {
      prefix = prefix.slice(0, -"/api/strm".length);
    }
    return prefix;
  };

  const form = useForm<TaskFormValues>({
    resolver: zodResolver(taskFormSchema),
    defaultValues: task ? {
      ...task,
      strmPrefix: getInitialStrmPrefix(),
    } : {
      account: "",
      originPath: "",
      targetPath: "",
      strmType: "local",
      strmPrefix: "",
      removeExtraFiles: true,
    },
  });

  // 监听表单值变化
  React.useEffect(() => {
    const subscription = form.watch((value) => {
      setFormValues({
        strmPrefix: value.strmPrefix || "",
        originPath: value.originPath || "",
        account: value.account || "",
      });
    });
    return () => subscription.unsubscribe();
  }, [form]);

  // 初始化时同步表单值到状态
  React.useEffect(() => {
    if (task) {
      let prefix = task.strmPrefix || "";
      if (task.account && prefix.endsWith("/" + task.account)) {
        prefix = prefix.slice(0, -(task.account.length + 1));
      }
      if (prefix.endsWith("/api/strm")) {
        prefix = prefix.slice(0, -"/api/strm".length);
      }
      setFormValues({
        strmPrefix: prefix,
        originPath: task.originPath || "",
        account: task.account || "",
      });
    }
  }, [task]);

  const onSubmit = async (values: TaskFormValues) => {
    setLoading(true);
    try {
      // 自动获取选中账户的类型
      const accountType = getAccountType(values.account);
      
      // strmPrefix 存储为纯前缀（不含账号名），302 拼接由 resolveStrmSettings 在运行时处理
      const taskData = {
        ...values,
        accountType,
      };

      if (task?.id) {
        await axiosInstance.put("/api/task", { id: task.id, ...taskData });
      } else {
        await axiosInstance.post("/api/task", taskData);
      }

      onSuccess?.();
      setOpen(false);
      form.reset();
    } finally {
      setLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        {trigger ?? (
          <Button variant="outline">{task ? "编辑" : "新增任务"}</Button>
        )}
      </DialogTrigger>

      <DialogContent className="max-w-[95vw] sm:max-w-[450px]">
        <DialogHeader>
          <DialogTitle>{task ? "编辑任务" : "新增任务"}</DialogTitle>
          <DialogDescription>
            {task ? "编辑现有任务" : "添加一个新的任务"}
          </DialogDescription>
        </DialogHeader>

        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
            {/* 账号 */}
            <FormField
              control={form.control}
              name="account"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>账户</FormLabel>
                  <FormControl>
                    <Select onValueChange={field.onChange} value={field.value}>
                      <SelectTrigger>
                        <SelectValue placeholder="选择账户" />
                      </SelectTrigger>
                      <SelectContent className="z-[60]">
                        {accountsLoading ? (
                          <SelectItem value="loading" disabled>
                            加载中...
                          </SelectItem>
                        ) : accounts.length === 0 ? (
                          <SelectItem value="no-accounts" disabled>
                            暂无账号
                          </SelectItem>
                        ) : (
                          accounts.map((acc) => (
                            <SelectItem key={acc.name} value={acc.name}>
                              {acc.name} ({acc.accountType})
                            </SelectItem>
                          ))
                        )}
                      </SelectContent>
                    </Select>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            {/* Origin Path */}
            <FormField
              control={form.control}
              name="originPath"
              render={({ field }) => (
                <FormItem>
                  <HelpLabel help="在这里输入网盘路径或 OpenList 的路径，如：tv 或 115/TV">
                    远程路径
                  </HelpLabel>
                  <FormControl>
                    <div className="flex items-center gap-2">
                      <Input {...field} placeholder="远程路径" className="flex-1" />
                      {is115Account && formValues.account && (
                        <Button
                          type="button"
                          variant="outline"
                          size="icon"
                          onClick={() => setDirectoryDialogOpen(true)}
                          title="选择目录"
                        >
                          <FolderOpen className="w-4 h-4" />
                        </Button>
                      )}
                    </div>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            {/* Target Path */}
            <FormField
              control={form.control}
              name="targetPath"
              render={({ field }) => (
                <FormItem>
                  <HelpLabel help="这里将生成 strm 文件到你的挂载目录里，比如填写 tv 将在挂载目录创建 tv 目录，并将 Origin Path 内所有的文件 strm 到 Target Path">
                    本地路径
                  </HelpLabel>
                  <FormControl>
                    <div className="flex items-center gap-2">
                      <Input {...field} placeholder="本地路径" className="flex-1" />
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        onClick={() => setLocalDirectoryDialogOpen(true)}
                        title="选择目录"
                      >
                        <FolderOpen className="w-4 h-4" />
                      </Button>
                    </div>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            {/* Remove Extra Files */}
            <FormField
              control={form.control}
              name="removeExtraFiles"
              render={({ field }) => (
                <FormItem>
                  <HelpLabel help="开启后，任务执行时会删除本地存在但远程不存在的文件">
                    删除多余文件
                  </HelpLabel>
                  <FormControl>
                    <div className="flex items-center space-x-2">
                      <input
                        type="checkbox"
                        id="removeExtraFiles"
                        checked={field.value || false}
                        onChange={(e) => field.onChange(e.target.checked)}
                        className="rounded border-gray-300"
                      />
                      <label htmlFor="removeExtraFiles" className="text-sm">
                        启用删除本地多余文件功能
                      </label>
                    </div>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            {/* 高级设置：STRM 前缀覆盖 */}
            <AdvancedStrmSettings
              open={advancedOpen}
              onToggle={() => setAdvancedOpen(!advancedOpen)}
              control={form.control}
              previewPath={getPreviewPath()}
            />

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

        {/* 目录树选择对话框 */}
        {is115Account && formValues.account && (
          <DirectoryTreeDialog
            open={directoryDialogOpen}
            onOpenChange={setDirectoryDialogOpen}
            account={formValues.account}
            onSelect={(path) => {
              form.setValue("originPath", path);
              setFormValues((prev) => ({ ...prev, originPath: path }));
            }}
          />
        )}

        {/* 本地目录树选择对话框 */}
        <LocalDirectoryTreeDialog
          open={localDirectoryDialogOpen}
          onOpenChange={setLocalDirectoryDialogOpen}
          onSelect={(path) => {
            form.setValue("targetPath", path);
            setFormValues((prev) => ({ ...prev, targetPath: path }));
          }}
        />
      </DialogContent>
    </Dialog>
  );
}
