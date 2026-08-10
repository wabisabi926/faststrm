import { NextRequest, NextResponse } from "next/server";
import { readAccounts, readSettings, readTasks } from "@/lib/serverUtils";
import {
  computeMediaMountEntries,
  syncMediaMountPaths,
  sourceTagLabel,
  type MediaMountEntry,
  type SyncResult,
} from "@/lib/mediaMountSync";

interface DryRunResponse {
  /** 当前 settings.json 里持久化的 mediaMountPath（旧快照） */
  persisted: string[];
  /** 最新计算出的"期望"集合（带来源 tag） */
  computed: Array<
    MediaMountEntry & { sourceLabel: string }
  >;
  /** 期望集合的纯路径数组 */
  final: string[];
  /** 相对持久化值的 diff */
  diff: {
    added: string[];
    removed: string[];
    kept: string[];
    changed: boolean;
  };
}

function tagWithLabel(
  entries: MediaMountEntry[]
): Array<MediaMountEntry & { sourceLabel: string }> {
  return entries.map((e) => ({ ...e, sourceLabel: sourceTagLabel(e.source) }));
}

export async function GET() {
  try {
    const settings = readSettings();
    const accounts = readAccounts() as unknown as { name: string }[];
    const tasks = readTasks() as Array<{
      id?: string;
      account?: string;
      strmPrefix?: string;
      enablePathEncoding?: boolean;
      enable302?: boolean;
    }>;

    const { entries, finalPaths } = computeMediaMountEntries({
      settings,
      accounts,
      tasks,
    });

    const persisted = Array.isArray(settings.mediaMountPath)
      ? settings.mediaMountPath
      : [];
    const persistedSet = new Set(persisted);
    const finalSet = new Set(finalPaths);

    const added = finalPaths.filter((p) => !persistedSet.has(p));
    const removed = persisted.filter((p) => !finalSet.has(p));
    const kept = finalPaths.filter((p) => persistedSet.has(p));

    const data: DryRunResponse = {
      persisted,
      computed: tagWithLabel(entries).sort((a, b) =>
        a.prefix.localeCompare(b.prefix)
      ),
      final: finalPaths,
      diff: {
        added,
        removed,
        kept,
        changed: added.length > 0 || removed.length > 0,
      },
    };

    return NextResponse.json(data);
  } catch (e) {
    const message = e instanceof Error ? e.message : String(e);
    return NextResponse.json(
      { error: message, persisted: [], computed: [], final: [], diff: { added: [], removed: [], kept: [], changed: false } },
      { status: 500 }
    );
  }
}

export async function POST(req: NextRequest) {
  let body: { skipNginxReload?: boolean } = {};
  try {
    body = (await req.json()) as { skipNginxReload?: boolean };
  } catch {
    body = {};
  }
  const result: SyncResult = await syncMediaMountPaths({
    skipNginxReload: body?.skipNginxReload === true,
  });
  return NextResponse.json(result);
}
