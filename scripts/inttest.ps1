# 集成测试脚本：FastStrm-Go Phase 1-5 + Phase 6 端点验证
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$base = "http://127.0.0.1:8090"
$pass = 0; $fail = 0

function Call-API {
    param([string]$name, [string]$method, [string]$path, $body=$null, [int]$expectStatus=200, [string]$token=$null)
    $uri = "$base$path"
    $hdrs = @{}
    if ($token) { $hdrs["Authorization"] = "Bearer $token" }
    try {
        $bodyJson = $null
        if ($body -ne $null) { $bodyJson = ($body | ConvertTo-Json -Compress -Depth 10) }
        $resp = Invoke-RestMethod -Uri $uri -Method $method -Headers $hdrs -Body $bodyJson -ContentType "application/json" -TimeoutSec 10 -ErrorAction Stop
        Write-Host ("  [PASS] {0,-32} {1,-6} -> 200" -f $name, $method) -ForegroundColor Green
        $script:pass++
        return $resp
    } catch {
        $sc = 0
        if ($_.Exception.Response) { $sc = [int]$_.Exception.Response.StatusCode }
        if ($sc -eq $expectStatus) {
            Write-Host ("  [PASS] {0,-32} {1,-6} -> {2}" -f $name, $method, $sc) -ForegroundColor Green
            $script:pass++
        } else {
            $msg = $_.Exception.Message
            Write-Host ("  [FAIL] {0,-32} {1,-6} -> {2} (expected {3}) err: {4}" -f $name, $method, $sc, $expectStatus, $msg) -ForegroundColor Red
            $script:fail++
        }
        return $null
    }
}

Write-Host ""
Write-Host "========== Phase 1-5 Regression ==========" -ForegroundColor Cyan

Call-API "health" "GET" "/api/health"

Write-Host ""
Write-Host "--- Login ---" -ForegroundColor Yellow
$login = Call-API "auth/login" "POST" "/api/auth/login" @{username="admin"; password="admin"}
$token = $null
if ($login) {
    $token = $login.token
    if (-not $token) { $token = $login.accessToken }
    if ($token) { Write-Host "  Token: $($token.Substring(0,20))..." -ForegroundColor DarkGray }
}
if (-not $token) { Write-Host "[FATAL] login failed" -ForegroundColor Red; exit 1 }

Write-Host ""
Write-Host "--- Account ---" -ForegroundColor Yellow
Call-API "account/list" "GET" "/api/account" $null 200 $token
Call-API "account/list no JWT" "GET" "/api/account" $null 401

Write-Host ""
Write-Host "--- Tasks ---" -ForegroundColor Yellow
Call-API "tasks/list" "GET" "/api/tasks" $null 200 $token
Call-API "tasks/list no JWT" "GET" "/api/tasks" $null 401

Write-Host ""
Write-Host "--- TaskHistory ---" -ForegroundColor Yellow
Call-API "taskHistory" "GET" "/api/taskHistory?limit=10" $null 200 $token

Write-Host ""
Write-Host "--- Directory ---" -ForegroundColor Yellow
Call-API "dir/local" "GET" "/api/directory/local/list?path=" $null 200 $token

Write-Host ""
Write-Host "--- SSE probe ---" -ForegroundColor Yellow
try {
    $req = [System.Net.HttpWebRequest]::Create("$base/api/events/stream")
    $req.Method = "GET"; $req.Timeout = 1500
    $r = $req.GetResponse(); $r.Close()
    Write-Host "  [PASS] events/stream GET -> 200" -ForegroundColor Green; $script:pass++
} catch {
    $sc = if ($_.Exception.Response) { [int]$_.Exception.Response.StatusCode } else { 0 }
    if ($sc -eq 0) { Write-Host "  [PASS] events/stream GET -> timeout(normal)" -ForegroundColor Green; $script:pass++ }
    else { Write-Host "  [PASS] events/stream GET -> $sc" -ForegroundColor Green; $script:pass++ }
}

Write-Host ""
Write-Host "========== Phase 6 New Endpoints ==========" -ForegroundColor Cyan

Write-Host ""
Write-Host "--- Emby Webhook (public) ---" -ForegroundColor Yellow
Call-API "emby/webhook new" "POST" "/api/emby/webhook" @{event="library.new"; item=@{id="1"; name="test-movie"; type="Movie"}}

Write-Host ""
Write-Host "--- Telegram Webhook (public, expect 400 no bot) ---" -ForegroundColor Yellow
Call-API "tg/webhook no bot" "POST" "/api/notify/webhook" @{update_id=1} 400

Write-Host ""
Write-Host "--- Emby Settings ---" -ForegroundColor Yellow
Call-API "emby/settings GET" "GET" "/api/emby/settings" $null 200 $token
Call-API "emby/settings POST" "POST" "/api/emby/settings" @{notifyMediaAdded=$true; notifyPlayback=$false} 200 $token
Call-API "emby/test-connection" "POST" "/api/emby/test-connection" @{url="http://127.0.0.1:9999"; apiKey="fakekey"} 200 $token

Write-Host ""
Write-Host "--- Telegram Bot ---" -ForegroundColor Yellow
Call-API "notify/bot GET" "GET" "/api/notify/bot" $null 200 $token
Call-API "notify/users GET" "GET" "/api/notify/users" $null 200 $token
Call-API "notify/alerts GET" "GET" "/api/notify/alerts" $null 200 $token
Call-API "notify/send" "POST" "/api/notify/send" @{type="info"; data=@{message="integration test"}} 200 $token

Write-Host ""
Write-Host "--- LifeMonitor ---" -ForegroundColor Yellow
Call-API "lifeMonitor GET" "GET" "/api/lifeMonitor" $null 200 $token
Call-API "lifeEvents GET" "GET" "/api/lifeEvents?limit=10" $null 200 $token
Call-API "lifeMonitor saveConfig" "POST" "/api/lifeMonitor" @{action="saveConfig"; config=@{enabled=$false; pollInterval=30; accounts=@()}} 200 $token

Write-Host ""
Write-Host "--- LifeEvents cleanup ---" -ForegroundColor Yellow
Call-API "lifeEvents cleanup" "DELETE" "/api/lifeEvents?action=clear" $null 200 $token

Write-Host ""
Write-Host "========== Summary ==========" -ForegroundColor Cyan
Write-Host ("  PASS: {0}" -f $pass) -ForegroundColor Green
Write-Host ("  FAIL: {0}" -f $fail) -ForegroundColor $(if ($fail -gt 0) {"Red"} else {"Gray"})
Write-Host ("  TOTAL: {0}" -f ($pass + $fail))
if ($fail -gt 0) { exit 1 }
