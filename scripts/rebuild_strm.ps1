<#
.SYNOPSIS
    扫描 .strm 文件，将旧的 /api/fs/get URL 统一替换为 /api/strm。
.DESCRIPTION
    方案 A 执行后，STRM 生成统一使用 /api/strm，旧 STRM 文件里还有 /api/fs/get。
    虽然后端 HandleFsGet 内部已转发到 HandleStrm（兼容旧文件），但建议主动更新。
.PARAMETER Path
    要扫描的目录（默认：脚本所在目录的父目录的 data/strm）
.PARAMETER DryRun
    干跑模式，只打印变更预览，不实际写文件
.EXAMPLE
    ./rebuild_strm.ps1                          # 扫描默认目录并实际替换
    ./rebuild_strm.ps1 -DryRun                  # 预览模式
    ./rebuild_strm.ps1 -Path "D:\Media\Strms"   # 指定目录
    ./rebuild_strm.ps1 -Path "/mnt/user/strms"  # 飞牛 Linux 路径
#>
param(
    [string]$Path,
    [switch]$DryRun
)

$ErrorActionPreference = "Continue"

# 默认路径：脚本上级/data/strm，飞牛常见路径 /mnt/user/appdata/faststrm/strm
if (-not $Path) {
    $candidate = Join-Path $PSScriptRoot "..\data\strm"
    if (Test-Path $candidate) {
        $Path = Resolve-Path $candidate
    } else {
        $Path = "."
    }
}

if (-not (Test-Path $Path)) {
    Write-Host "[ERROR] 目录不存在: $Path" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "============================================" -ForegroundColor Cyan
Write-Host "  STRM URL 重建脚本  ( /api/fs/get → /api/strm )" -ForegroundColor Cyan
Write-Host "============================================" -ForegroundColor Cyan
Write-Host "  扫描目录 : $Path"
Write-Host "  干跑模式 : $DryRun"
Write-Host "============================================" -ForegroundColor Cyan
Write-Host ""

# 收集所有 .strm 文件
$files = Get-ChildItem -Path $Path -Filter "*.strm" -File -Recurse
Write-Host "找到 $($files.Count) 个 .strm 文件" -ForegroundColor Yellow

$changed = 0
$skipped = 0
$noChange = 0
$errors = 0

foreach ($f in $files) {
    try {
        $content = [System.IO.File]::ReadAllText($f.FullName)

        # 核心替换：/api/fs/get → /api/strm
        # 注意：只替换路径部分，保留查询参数
        $newContent = $content -replace '/api/fs/get(\?|$)', '/api/strm$1'

        # 也兼容 fs_get / fsget / fs-get 等写法（以防万一）
        $newContent = $newContent -replace '/api/fs[_-]?get(\?|$)', '/api/strm$1'

        if ($newContent -eq $content) {
            $noChange++
            continue
        }

        $relPath = $f.FullName.Replace("$($Path)\", "")
        $oldLine = ($content.Trim() -split "`n")[0]
        $newLine = ($newContent.Trim() -split "`n")[0]

        if ($DryRun) {
            Write-Host "  [DRY] $relPath" -ForegroundColor Gray
            Write-Host "       - OLD: $oldLine" -ForegroundColor DarkGray
            Write-Host "       + NEW: $newLine" -ForegroundColor Green
        } else {
            [System.IO.File]::WriteAllText($f.FullName, $newContent, [System.Text.UTF8Encoding]::new($false))
            Write-Host "  [OK] $relPath" -ForegroundColor Green
            Write-Host "       - OLD: $oldLine" -ForegroundColor DarkGray
            Write-Host "       + NEW: $newLine" -ForegroundColor Green
        }
        $changed++
    } catch {
        Write-Host "  [ERR] $($f.FullName) — $_" -ForegroundColor Red
        $errors++
    }
}

Write-Host ""
Write-Host "============================================" -ForegroundColor Cyan
Write-Host "  汇总"
Write-Host "============================================" -ForegroundColor Cyan
Write-Host "  扫描文件  : $($files.Count)"
Write-Host "  已更新    : $changed" -ForegroundColor Green
Write-Host "  无变化    : $noChange"
Write-Host "  跳过/错误 : $errors" -ForegroundColor Red
Write-Host "============================================" -ForegroundColor Cyan

if ($DryRun -and $changed -gt 0) {
    Write-Host ""
    Write-Host "提示：去掉 -DryRun 参数即可实际写入文件" -ForegroundColor Yellow
}

if ($changed -gt 0 -and -not $DryRun) {
    Write-Host ""
    Write-Host "✅ 全部完成！建议重启 FastStrm 服务使新 STRM 生效。" -ForegroundColor Green
}
