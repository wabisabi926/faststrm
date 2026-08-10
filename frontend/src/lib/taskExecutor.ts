import { from, mergeMap, Subject, Subscription } from "rxjs";
import { downloadTasks, DownloadProgress } from "@/lib/downloadTaskManager";
import * as fs from "fs";
import * as path from "path";
import axios from "axios";
import {
  buildTree,
  collectFilesAndTopEmptyDirs,
  getLocalTree,
  normalizeToStrm,
  readTasks,
  saveTasks,
  removeExtraFiles,
  notifyEmbyRefresh,
  readAccounts,
  readSettings,
  resolveStrmSettings,
  getStrmExtensions,
} from "@/lib/serverUtils";
import {
  createTaskExecution,
  updateTaskExecution,
  addLogToTaskExecution,
  completeTaskExecution,
} from "@/lib/taskHistoryManager";
import {
  getRealDownloadLink,
  downloadOrCreateStrmLimited,
  downloadOrCreateStrm,
} from "@/lib/enqueueForAccount";
import { exportDirParse, fs_dir_getid } from "@/lib/115";
import type { AccountInfo } from "@/lib/115";
import { getFilePathEntryByPath } from "@/lib/filePathDb";
import { sendTelegramNotification } from "@/lib/telegram";
import { encryptAccounts } from "@/lib/passwordCrypto";
import { TaskSchedule } from "@/lib/taskScheduler";
import {
  tryEnterFullScan,
  exitFullScan,
  suspendMonitorForFullScan,
  AccountRuntimeState,
} from "./accountRuntimeState";

interface OpenlistItem {
  name: string;
  is_dir: boolean;
  size?: number;
  modified?: string;
}

interface OpenlistResponse {
  code: number;
  message: string;
  data: {
    content: OpenlistItem[];
  };
}

type TreeNode = {
  depth: number;
  key: number;
  name: string;
  parent_key: number;
};

type TreeEntry = TreeNode & { children?: TreeEntry[] };

type ExecTask = {
  id: string;
  account: string;
  accountType?: string;
  originPath: string;
  targetPath: string;
  strmPrefix?: string;
  enablePathEncoding?: boolean;
  enable302?: boolean;
  removeExtraFiles?: boolean;
  schedule?: TaskSchedule;
};

type ScheduleTask = {
  id: string;
  schedule?: TaskSchedule & Record<string, unknown>;
};

function readScheduledTasks(): ScheduleTask[] {
  return readTasks() as ScheduleTask[];
}

async function getOpenlistTreeData(
  baseUrl: string,
  token: string,
  originPath: string
): Promise<TreeNode[]> {
  const allPaths: string[] = [];

  async function collectPaths(currentPath: string): Promise<void> {
    const response = await axios.post<OpenlistResponse>(
      `${baseUrl}/api/fs/list`,
      {
        path: currentPath,
        page: 1,
        per_page: 0,
        refresh: true,
      },
      {
        headers: { Authorization: token },
      }
    );

    if (response.data.code !== 200) {
      throw new Error(
        `Failed to list directory ${currentPath}: ${response.data.message}`
      );
    }

    const items = response.data.data.content || [];

    for (const item of items) {
      const itemPath = buildPath(currentPath, item.name);
      allPaths.push(itemPath);

      if (item.is_dir) {
        await collectPaths(itemPath);
      }
    }
  }

  function buildPath(basePath: string, itemName: string): string {
    if (basePath === "/" || basePath === "") {
      return itemName;
    }
    return basePath.endsWith("/")
      ? `${basePath}${itemName}`
      : `${basePath}/${itemName}`;
  }

  function cleanPaths(paths: string[]): string[] {
    const pathParts = originPath.split("/").filter((part) => part !== "");
    const lastDir = pathParts[pathParts.length - 1] || "";
    const prefixToRemove = originPath.substring(
      0,
      originPath.lastIndexOf("/" + lastDir)
    );

    return paths
      .map((path) => {
        if (prefixToRemove.length === 0) return path;

        if (path.startsWith(prefixToRemove + "/")) {
          return path.substring(prefixToRemove.length + 1);
        }
        if (path.startsWith(prefixToRemove)) {
          const cleaned = path.substring(prefixToRemove.length);
          return cleaned.startsWith("/") ? cleaned.substring(1) : cleaned;
        }
        return path;
      })
      .filter((path) => path.length > 0);
  }

  function convertToFlatFormat(paths: string[]): TreeNode[] {
    const treeData: TreeNode[] = [];
    const nodeMap = new Map<string, number>();
    let keyCounter = 1;

    treeData.push({ depth: 0, key: 0, name: "", parent_key: 0 });

    for (const fullPath of paths) {
      const pathParts = fullPath.split("/").filter((part) => part !== "");
      let parentKey = 0;
      let currentPath = "";

      for (let i = 0; i < pathParts.length; i++) {
        const name = pathParts[i].trim();
        const depth = i + 1;
        currentPath = i === 0 ? name : `${currentPath}/${name}`;

        const nodeKey = `${depth}-${name}-${parentKey}`;
        if (!nodeMap.has(nodeKey)) {
          const newNode: TreeNode = {
            depth,
            key: keyCounter++,
            name,
            parent_key: parentKey,
          };
          treeData.push(newNode);
          nodeMap.set(nodeKey, newNode.key);
          parentKey = newNode.key;
        } else {
          parentKey = nodeMap.get(nodeKey)!;
        }
      }
    }

    return treeData;
  }

  try {
    await collectPaths(originPath);
    const cleanedPaths = cleanPaths(allPaths);
    return convertToFlatFormat(cleanedPaths);
  } catch (error) {
    if (axios.isAxiosError(error)) {
      throw new Error(
        `Openlist API error: ${error.response?.statusText || error.message}`
      );
    }
    throw error;
  }
}

function startDownloadTask({
  filePaths,
  saveDir,
  account,
  taskId,
  strmPrefix,
  originPath,
  targetPath,
  removeExtraFiles,
  enablePathEncoding,
  enable302,
}: {
  filePaths: string[];
  saveDir: string;
  account: string;
  taskId: string;
  strmPrefix?: string;
  originPath: string;
  targetPath: string;
  removeExtraFiles?: boolean;
  enablePathEncoding?: boolean;
  enable302?: boolean;
}): string {
  suspendMonitorForFullScan(account);
  const total = filePaths.length;
  const taskSubject = new Subject<DownloadProgress>();
  const perFile = new Map<string, number>();
  for (const fp of filePaths) perFile.set(fp, 0);

  const executionHistory = createTaskExecution(taskId, {
    account,
    originPath,
    targetPath,
    removeExtraFiles,
  });

  updateTaskExecution(executionHistory.id, {
    summary: {
      totalFiles: total,
      downloadedFiles: 0,
      deletedFiles: 0,
    },
  });

  downloadTasks[taskId] = {
    subject: taskSubject,
    subscription: new Subscription(),
    logs: [],
  };

  const startMessage =
    `<b>Task ID:</b> ${taskId}\n` +
    `<b>Account:</b> ${account}\n` +
    `<b>Target Path:</b> ${targetPath}\n` +
    `<b>Files to Download:</b> ${total}\n` +
    `<b>Origin Path:</b> ${originPath}`;

  sendTelegramNotification(startMessage, "start");

  const lastRecordedProgress = new Map<string, number>();

  const pushLog = (log: DownloadProgress) => {
    const line = JSON.stringify(log);
    const task = downloadTasks[taskId];

    if (task) {
      task.logs.push(line);
      if (task.logs.length > 20000) task.logs.shift();
      task.subject.next(log);
    }

    let shouldRecordToHistory = false;

    if (log.filePath && log.percent !== undefined) {
      if (log.percent === 100) {
        shouldRecordToHistory = true;
        lastRecordedProgress.set(log.filePath, log.percent);
      }
    } else if (log.done || log.error) {
      shouldRecordToHistory = true;
    }

    if (shouldRecordToHistory) {
      addLogToTaskExecution(executionHistory.id, line);
    }
  };

  const settings = readSettings();
  const strmExtensions = getStrmExtensions().map((ext) =>
    ext.toLowerCase()
  );
  const downloadExtensions = (settings.downloadExtensions || []).map((ext) =>
    ext.toLowerCase()
  );

  const strmFiles = filePaths.filter((fp) =>
    strmExtensions.includes(path.extname(fp).toLowerCase())
  );
  strmFiles.forEach((filePath) => {
    const savePath = path.join(saveDir, filePath);
    const cloudPath = originPath + "/" + filePath;

    // 302 模式下尝试从 filePathDb 反查 pickcode
    let pickcode: string | undefined;
    if (enable302) {
      try {
        const entry = getFilePathEntryByPath(account, cloudPath);
        if (entry?.pickCode) pickcode = entry.pickCode;
      } catch {}
    }

    downloadOrCreateStrm(cloudPath, savePath, {
      asStrm: true,
      displayPath: filePath,
      strmPrefix,
      enablePathEncoding,
      enable302,
      account,
      pickcode,
      fileName: path.basename(filePath),
    }).subscribe({
      next: (p) => {
        perFile.set(p.filePath!, 100);
        pushLog({ filePath: p.filePath, percent: 100 });
      },
      error: (err) => pushLog({ error: err.message }),
    });
  });

  const downloadFiles = filePaths.filter((fp) =>
    downloadExtensions.includes(path.extname(fp).toLowerCase())
  );
  console.log("downloadFiles: ", downloadFiles);
  const subscription = from(downloadFiles)
    .pipe(
      mergeMap(
        (filePath) =>
          from(
            getRealDownloadLink(originPath + "/" + filePath, account)
          ).pipe(
            mergeMap((url) =>
              downloadOrCreateStrmLimited(
                url,
                path.join(saveDir, filePath),
                account,
                {
                  asStrm: false,
                  displayPath: filePath,
                }
              )
            )
          ),
        10
      )
    )
    .subscribe({
      next: (p) => {
        perFile.set(p.filePath!, Math.min(100, Math.max(0, p.percent)));
        const sum = [...perFile.values()].reduce((a, b) => a + b, 0);
        const overallPercent = (sum / total).toFixed(2);
        pushLog({ filePath: p.filePath, percent: p.percent, overallPercent });
      },
      complete: () => {
        pushLog({ done: true, overallPercent: "100.00" });
        taskSubject.complete();

        const completeMessage =
          `<b>Task ID:</b> ${taskId}\n` +
          `<b>Account:</b> ${account}\n` +
          `<b>Target Path:</b> ${targetPath}\n` +
          `<b>Files Downloaded:</b> ${total}\n` +
          `<b>Status:</b> Successfully completed`;

        sendTelegramNotification(completeMessage, "complete");

        completeTaskExecution(executionHistory.id, "completed", {
          totalFiles: total,
          downloadedFiles: total,
        });

        notifyEmbyRefresh();
        exitFullScan(account);
        delete downloadTasks[taskId];
      },
      error: (err) => {
        pushLog({ error: err.message });

        const errorMessage =
          `<b>Task ID:</b> ${taskId}\n` +
          `<b>Account:</b> ${account}\n` +
          `<b>Target Path:</b> ${targetPath}\n` +
          `<b>Error:</b> ${err.message}\n` +
          `<b>Status:</b> Failed`;

        sendTelegramNotification(errorMessage, "error");

        completeTaskExecution(executionHistory.id, "failed", {
          totalFiles: total,
          downloadedFiles: 0,
          errorMessage: err.message,
        });

        taskSubject.complete();
        exitFullScan(account);
        delete downloadTasks[taskId];
      },
    });
  if (downloadTasks[taskId]) {
    downloadTasks[taskId].subscription = subscription;
  }
  return taskId;
}

export interface ExecuteResult {
  success: boolean;
  blocked: boolean;
  reason?: string;
  message: string;
  taskId?: string;
  missingLocallyCount?: number;
  extraLocallyCount?: number;
  willDeleteExtraFiles?: boolean;
  error?: unknown;
}

export async function executeTask(
  taskId: string
): Promise<ExecuteResult> {
  let task: ExecTask | null = null;
  try {
    const tasks = readTasks() as ExecTask[];
    task = tasks.find((t: ExecTask) => t.id === taskId) ?? null;

    if (!task) {
      return {
        success: false,
        blocked: false,
        reason: "not_found",
        message: "Task not found",
      };
    }

    const enterResult = tryEnterFullScan(task.account, taskId);
    if (!enterResult.ok) {
      return {
        success: false,
        blocked: true,
        reason: "account_running",
        message: "Account already has an active task",
      };
    }

    const { account, originPath, targetPath } = task;

    // 使用统一的 STRM 设置解析（全局默认 + 任务级覆盖 + 302 拼接）
    const settings = readSettings();
    const resolvedStrm = resolveStrmSettings(account, task, settings);
    const strmPrefix = resolvedStrm.strmPrefix;
    const enablePathEncoding = resolvedStrm.enablePathEncoding;
    const enable302 = resolvedStrm.enable302;

    const accounts = readAccounts() as unknown as AccountInfo[];
    const accountInfo = accounts.find(
      (acc) => acc.name === account
    );
    if (!accountInfo) {
      throw new Error(`No account found: ${account}`);
    }

    const accountType = accountInfo.accountType;

    let tree: TreeEntry[] | undefined;
    if (accountType === "115") {
      if (!accountInfo.cookie) {
        throw new Error(`Missing cookie for 115 account: ${account}`);
      }

      const idRes = await fs_dir_getid(originPath, { accountInfo });

      try {
        const data = await exportDirParse({
          exportFileIds: idRes.id,
          targetPid: 0,
          layerLimit: 0,
          deleteAfter: true,
          timeoutMs: 300000,
          checkIntervalMs: 1000,
          accountInfo,
        });
        console.log("data: ", data);
        tree = buildTree(data) as TreeEntry[];
      } catch (error) {
        console.error("Failed to parse 115 directory: ", error);
        const errorMessage = error instanceof Error ? error.message : String(error);

        if (
          errorMessage.includes("<!doctypehtml>") ||
          errorMessage.includes("405") ||
          errorMessage.includes("您的访问被阻断") ||
          errorMessage.includes("potential threats to the server")
        ) {
          throw new Error("115账号被封控：账号访问被阿里云阻断，请检查账号状态或稍后重试");
        }

        throw new Error(`Failed to parse 115 directory: ${errorMessage}`);
      }
    } else if (accountType === "openlist") {
      if (!accountInfo.account || !accountInfo.password || !accountInfo.url) {
        throw new Error(`Missing openlist credentials for account: ${account}`);
      }

      let token = accountInfo.token;

      if (
        !token ||
        (accountInfo.expiresAt && Date.now() / 1000 > accountInfo.expiresAt)
      ) {
        try {
          const loginResponse = await axios.post(
            `${accountInfo.url}/api/auth/login`,
            {
              username: accountInfo.account,
              password: accountInfo.password,
            }
          );

          const loginData = loginResponse.data;
          console.log("loginData: ", loginData);
          if (loginData.code !== 200) {
            throw new Error(`Openlist login failed: ${loginData.message}`);
          }

          token = String(loginData.data.token);
        } catch (error) {
          if (axios.isAxiosError(error)) {
            throw new Error(
              `Failed to login to openlist: ${
                error.response?.statusText || error.message
              }`
            );
          }
          throw error;
        }

        accountInfo.token = token;
        accountInfo.expiresAt = Math.floor(Date.now() / 1000) + 47 * 60 * 60;

        const accountPath = path.join(process.cwd(), "../config/account.json");
        const encryptedAccounts = encryptAccounts(
          JSON.parse(JSON.stringify(accounts))
        );
        fs.writeFileSync(
          accountPath,
          JSON.stringify(encryptedAccounts, null, 2)
        );
      }

      const openlistTreeData = await getOpenlistTreeData(
        accountInfo.url,
        token,
        originPath
      );
      tree = buildTree(openlistTreeData) as TreeEntry[];
    }

    const saveDir = path.resolve(process.cwd(), `../data/${targetPath}`);
    if (!fs.existsSync(saveDir)) fs.mkdirSync(saveDir, { recursive: true });

    const remoteFiles: string[] = [];
    for (const node of tree || []) {
      if (node.children && node.children.length > 0) {
        remoteFiles.push(...collectFilesAndTopEmptyDirs(node.children));
      } else if (/\.[a-z0-9]+$/i.test(node.name)) {
        remoteFiles.push(node.name);
      }
    }
    const remotePaths = new Set(remoteFiles.map(normalizeToStrm));
    const localPaths = new Set(
      collectFilesAndTopEmptyDirs(getLocalTree(saveDir))
    );

    const missingLocally = remoteFiles.filter(
      (p) => !localPaths.has(normalizeToStrm(p))
    );
    const extraLocally = [...localPaths].filter((p) => !remotePaths.has(p));

    if (task.removeExtraFiles) {
      removeExtraFiles(extraLocally, saveDir);
    }

    if (missingLocally.length === 0) {
      exitFullScan(task.account);
      return {
        success: true,
        blocked: false,
        message: "no files to download",
        taskId,
        missingLocallyCount: 0,
        extraLocallyCount: extraLocally.length,
        willDeleteExtraFiles: task.removeExtraFiles || false,
      };
    }

    console.log("missingLocally: ", missingLocally);
    startDownloadTask({
      originPath: task.originPath,
      targetPath: task.targetPath,
      filePaths: missingLocally,
      saveDir,
      account: task.account,
      taskId: task.id,
      strmPrefix,
      removeExtraFiles: task.removeExtraFiles,
      enablePathEncoding,
      enable302,
    });

    const deleteMessage = task.removeExtraFiles
      ? `${extraLocally.length} files to delete.`
      : `${extraLocally.length} extra files found (not deleted due to task settings).`;

    return {
      success: true,
      blocked: false,
      message: `${missingLocally.length} files to download for task, ${deleteMessage}`,
      taskId: task.id,
      missingLocallyCount: missingLocally.length,
      extraLocallyCount: extraLocally.length,
      willDeleteExtraFiles: task.removeExtraFiles || false,
    };
  } catch (error) {
    console.error("executeTask error: ", error);
    if (task) {
      exitFullScan(task.account);
    }
    const errorMessage = error instanceof Error ? error.message : String(error);
    return {
      success: false,
      blocked: false,
      message: errorMessage,
      error,
    };
  }
}

export function updateTaskScheduleState(
  taskId: string,
  state: Partial<AccountRuntimeState> & {
    lastRunAt?: number;
    nextRunAt?: number;
    lastRunStatus?: string;
    lastRunMessage?: string;
    lastRunDurationMs?: number;
  }
) {
  const tasks = readScheduledTasks();
  const idx = tasks.findIndex((t) => t.id === taskId);
  if (idx === -1) return;
  if (!tasks[idx].schedule) tasks[idx].schedule = {};
  Object.assign(tasks[idx].schedule, state);
  saveTasks(tasks);
}
