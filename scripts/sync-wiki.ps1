param(
    [string]$Owner = 'wabisabi926',
    [string]$Repo = 'faststrm',
    [string]$Token = $env:GITHUB_TOKEN,
    [switch]$DryRun
)

$WikiDir = Join-Path $PSScriptRoot '..\wiki_drafts'
$ApiBase = "https://api.github.com/repos/$Owner/$Repo/wiki"

if (-not $Token) {
    Write-Host "ERROR: GitHub Token not provided" -ForegroundColor Red
    Write-Host "  Usage: .\sync-wiki.ps1 -Token ghp_xxx"
    Write-Host "  Or: $env:GITHUB_TOKEN = 'ghp_xxx'"
    Write-Host ""
    Write-Host "  Token: https://github.com/settings/tokens (need wiki:write)"
    exit 1
}

if (-not (Test-Path $WikiDir)) {
    Write-Host "ERROR: wiki_drafts not found: $WikiDir" -ForegroundColor Red
    exit 1
}

$headers = @{
    "Authorization"  = "Bearer $Token"
    "Accept"         = "application/vnd.github+json"
    "Content-Type"   = "application/json"
    "User-Agent"     = "faststrm-wiki-sync"
}

$stats = @{ Success = 0; Failed = 0; Skipped = 0 }

Write-Host ""
Write-Host "=== GitHub Wiki Sync ===" -ForegroundColor Cyan
Write-Host "Repo: $Owner/$Repo" -ForegroundColor Gray
Write-Host "Dir:  $WikiDir" -ForegroundColor Gray
if ($DryRun) { Write-Host "Mode: DRY RUN (no actual push)" -ForegroundColor Yellow }
Write-Host ""

$files = Get-ChildItem -Path $WikiDir -Filter '*.md' -File | Sort-Object Name

foreach ($file in $files) {
    $fileName = $file.Name
    $content  = Get-Content -Path $file.FullName -Raw -Encoding UTF8

    # Use filename (minus .md) as wiki page slug
    $slug = $fileName -replace '\.md$', ''
    $slugEncoded = [System.Uri]::EscapeDataString($slug)

    Write-Host "Push: $fileName -> [$slug]" -NoNewline

    if ($DryRun) {
        Write-Host "  [DRY RUN] skip" -ForegroundColor Yellow
        $stats.Skipped++
        continue
    }

    try {
        # Try to get existing page
        $getUrl = "$ApiBase/$slugEncoded"
        $sha = $null
        try {
            $existing = Invoke-RestMethod -Uri $getUrl -Headers $headers -Method Get -ErrorAction Stop
            $sha = $existing.sha
        } catch {}

        $bodyObj = @{
            "content" = $content
            "title"   = $slug
            "message" = "docs(wiki): sync $fileName via sync-wiki.ps1"
        }

        if ($sha) {
            $bodyObj | Add-Member -NotePropertyName 'sha' -NotePropertyValue $sha -Force
            $method = 'Put'
            $url = $getUrl
        } else {
            $method = 'Post'
            $url = $ApiBase
        }

        $body = $bodyObj | ConvertTo-Json -Depth 3
        Invoke-RestMethod -Uri $url -Headers $headers -Method $method -Body $body -ErrorAction Stop

        $stats.Success++
        Write-Host "  OK ($method)" -ForegroundColor Green
    } catch {
        $stats.Failed++
        Write-Host "  FAIL" -ForegroundColor Red
        $errMsg = $_.Exception.Message
        if ($_.Exception.Response) {
            $reader = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream())
            $errMsg = "$errMsg -- $($reader.ReadToEnd())"
        }
        Write-Host "  Error: $errMsg" -ForegroundColor Red
    }

    Start-Sleep -Milliseconds 300
}

Write-Host ""
Write-Host "=== Done ===" -ForegroundColor Cyan
Write-Host "Success: $($stats.Success)  Failed: $($stats.Failed)  Skipped: $($stats.Skipped)"
Write-Host "Wiki: https://github.com/$Owner/$Repo/wiki"
if ($DryRun) { Write-Host "Tip: remove -DryRun to actually push." -ForegroundColor Yellow }