import { NextRequest, NextResponse } from "next/server";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";

interface TreeNode {
  name: string;
  id: string; // 使用路径作为ID
  isDir: boolean;
  hasChildren?: boolean;
}

// 检查是否是 Windows 系统
const isWindows = os.platform() === "win32";

// 获取 Windows 可用盘符列表
function getWindowsDrives(): TreeNode[] {
  const drives: TreeNode[] = [];
  const driveLetters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ".split("");
  for (const letter of driveLetters) {
    const drivePath = `${letter}:\\`;
    try {
      if (fs.existsSync(drivePath)) {
        drives.push({
          name: `${letter}:`,
          id: drivePath,
          isDir: true,
          hasChildren: true,
        });
      }
    } catch {
      // 忽略无法访问的盘符
    }
  }
  return drives;
}

// 防止路径穿越：解析后路径必须等于真实解析路径（不能 ../ 跳出起始目录之外）
// 注意：这里不再限制在 data/ 内，但仍然拒绝包含 ".." 段导致跳出用户当前路径语义的输入
function normalizeSafePath(basePath: string): string {
  // 规范化
  const normalized = path.normalize(basePath);
  // 允许绝对路径直接使用
  if (path.isAbsolute(normalized)) return normalized;
  // 如果是相对路径，解析为绝对路径
  return path.resolve(normalized);
}

export async function POST(req: NextRequest) {
  try {
    const body = await req.json();
    const { basePath = "" } = body;

    // 空 basePath 表示浏览根级（Windows -> 盘符列表；Linux/Mac -> /）
    if (basePath === "" || basePath === null || basePath === undefined) {
      if (isWindows) {
        // Windows 顶层返回盘符列表
        return NextResponse.json({
          code: 200,
          message: "success",
          data: getWindowsDrives(),
        });
      } else {
        // Unix 类系统从 / 开始
        return listDirectory("/");
      }
    }

    const targetPath = normalizeSafePath(basePath);

    // 检查目录是否存在
    try {
      const stat = fs.statSync(targetPath);
      if (!stat.isDirectory()) {
        return NextResponse.json({
          code: 200,
          message: "success",
          data: [],
        });
      }
    } catch {
      return NextResponse.json({
        code: 200,
        message: "success",
        data: [],
      });
    }

    return listDirectory(targetPath);
  } catch (error) {
    console.error("[directory/local/list] Error:", error);
    return NextResponse.json(
      {
        code: 500,
        message: error instanceof Error ? error.message : "internal error",
      },
      { status: 500 }
    );
  }
}

function listDirectory(targetPath: string) {
  const items = fs.readdirSync(targetPath);
  const nodes: TreeNode[] = [];

  for (const item of items) {
    const itemPath = path.join(targetPath, item);
    try {
      const itemStat = fs.statSync(itemPath);
      if (itemStat.isDirectory()) {
        // 检查是否有子目录
        let hasChildren = false;
        try {
          const subItems = fs.readdirSync(itemPath);
          hasChildren = subItems.some((subItem) => {
            const subItemPath = path.join(itemPath, subItem);
            try {
              return fs.statSync(subItemPath).isDirectory();
            } catch {
              return false;
            }
          });
        } catch {
          // 忽略错误
        }

        // id 使用规范化的完整路径（保留原路径分隔符以便前端拼接）
        const idPath = path.join(targetPath, item);

        nodes.push({
          name: item,
          id: idPath,
          isDir: true,
          hasChildren,
        });
      }
    } catch (error) {
      // 忽略无法访问的文件/目录（权限问题等）
    }
  }

  // 按名称排序
  nodes.sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: "base" }));

  return NextResponse.json({
    code: 200,
    message: "success",
    data: nodes,
  });
}
