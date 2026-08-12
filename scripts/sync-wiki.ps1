param(
    [string]$Owner = 'wabisabi926',
    [string]$Repo = 'faststrm',
    [string]$Token = $env:GITHUB_TOKEN,
    [string]$GitUserName = 'github-actions[bot]',
    [string]$GitUserEmail = '41898282+github-actions[bot]@users.noreply.github.com',
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'

$WikiDraftsDir = Join-Path $PSScriptRoot '..\wiki_drafts'
$WorkDir       = Join-Path $PSScriptRoot '..\.wiki-sync-tmp'

if (-not $Token -and -not $DryRun) {
    Write-Host "ERROR: GitHub Token not provided" -ForegroundColor Red
    Write-Host "  Usage: .\sync-wiki.ps1 -Token ghp_xxx"
    Write-Host "  Or: `$env:GITHUB_TOKEN = 'ghp_xxx'"
    Write-Host ""
    Write-Host "  Token needs 'repo' scope (to push to $Repo.wiki.git)"
    exit 1
}

if (-not (Test-Path $WikiDraftsDir)) {
    Write-Host "ERROR: wiki_drafts not found: $WikiDraftsDir" -ForegroundColor Red
    exit 1
}

# Build wiki.git URL with token auth
if ($Token) {
    $WikiGitUrl = "https://x-access-token:$Token@github.com/$Owner/$Repo.wiki.git"
    $DisplayUrl = "https://***@github.com/$Owner/$Repo.wiki.git"
} else {
    $WikiGitUrl = "https://github.com/$Owner/$Repo.wiki.git"
    $DisplayUrl = $WikiGitUrl
}

Write-Host ""
Write-Host "=== GitHub Wiki Sync (Git Mode) ===" -ForegroundColor Cyan
Write-Host "Repo:     $Owner/$Repo" -ForegroundColor Gray
Write-Host "Wiki URL: https://github.com/$Owner/$Repo/wiki" -ForegroundColor Gray
Write-Host "Drafts:   $WikiDraftsDir" -ForegroundColor Gray
if ($DryRun) { Write-Host "Mode:     DRY RUN (no clone / commit / push)" -ForegroundColor Yellow }
Write-Host ""

# Step 1: Clean & clone
Write-Host "> Step 1: Clone wiki repo..." -ForegroundColor Cyan
if (Test-Path $WorkDir) {
    Remove-Item $WorkDir -Recurse -Force
}

if ($DryRun) {
    Write-Host "  [DRY RUN] git clone --depth=1 $DisplayUrl $WorkDir"
    New-Item -ItemType Directory -Path $WorkDir | Out-Null
} else {
    git clone --depth=1 $WikiGitUrl $WorkDir
    if ($LASTEXITCODE -ne 0) { throw "git clone failed (exit $LASTEXITCODE)" }
}

# Step 2: Configure git identity
Write-Host ""
Write-Host "> Step 2: Configure git identity..." -ForegroundColor Cyan
if ($DryRun) {
    Write-Host "  [DRY RUN] git -C `"$WorkDir`" config user.name `"$GitUserName`""
    Write-Host "  [DRY RUN] git -C `"$WorkDir`" config user.email `"$GitUserEmail`""
} else {
    git -C $WorkDir config user.name  $GitUserName
    git -C $WorkDir config user.email $GitUserEmail
}

# Step 3: Sync .md files
Write-Host ""
Write-Host "> Step 3: Sync wiki_drafts/*.md into cloned wiki..." -ForegroundColor Cyan

# Remove existing .md files
$existing = if ($DryRun) { @() } else { Get-ChildItem -Path $WorkDir -Filter '*.md' -File }
foreach ($f in $existing) {
    if ($DryRun) { Write-Host "  [DRY RUN] rm $($f.Name)" }
    else {
        Remove-Item $f.FullName -Force
        Write-Host "  removed $($f.Name)"
    }
}

# Copy new drafts
$drafts = Get-ChildItem -Path $WikiDraftsDir -Filter '*.md' -File | Sort-Object Name
foreach ($f in $drafts) {
    $dst = Join-Path $WorkDir $f.Name
    if ($DryRun) { Write-Host "  [DRY RUN] cp $($f.Name) -> $dst" }
    else {
        Copy-Item -Path $f.FullName -Destination $dst -Force
        Write-Host "  copied  $($f.Name)"
    }
}

# Step 4: Commit & push
Write-Host ""
Write-Host "> Step 4: Commit & push..." -ForegroundColor Cyan

$status = if ($DryRun) { "?? Home.md" } else { git -C $WorkDir status --porcelain }

if (-not $status) {
    Write-Host "  No changes detected, skip commit & push."
} elseif ($DryRun) {
    Write-Host "  [DRY RUN] git -C `"$WorkDir`" add -A"
    Write-Host "  [DRY RUN] git -C `"$WorkDir`" commit -m `"docs(wiki): sync pages via sync-wiki.ps1`""
    Write-Host "  [DRY RUN] git -C `"$WorkDir`" push origin HEAD"
} else {
    git -C $WorkDir add -A
    git -C $WorkDir commit -m "docs(wiki): sync pages via sync-wiki.ps1"
    if ($LASTEXITCODE -ne 0) { throw "git commit failed (exit $LASTEXITCODE)" }
    git -C $WorkDir push origin HEAD
    if ($LASTEXITCODE -ne 0) { throw "git push failed (exit $LASTEXITCODE)" }
    Write-Host "  Push success ✅" -ForegroundColor Green
}

# Step 5: Cleanup
Write-Host ""
Write-Host "> Step 5: Cleanup working dir..." -ForegroundColor Cyan
if ((Test-Path $WorkDir) -and -not $DryRun) {
    Remove-Item $WorkDir -Recurse -Force
    Write-Host "  Done."
}

Write-Host ""
Write-Host "=== Done ===" -ForegroundColor Cyan
Write-Host "Wiki: https://github.com/$Owner/$Repo/wiki"
if ($DryRun) { Write-Host "Tip: remove -DryRun to actually push." -ForegroundColor Yellow }
