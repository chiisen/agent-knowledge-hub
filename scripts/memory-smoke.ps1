#requires -Version 7.0
# Run in a separate pwsh process: this script returns an exit code.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$repo = Split-Path -Parent $PSScriptRoot
$tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$work = Join-Path $tempRoot ('akh-memory-smoke-' + [guid]::NewGuid().ToString('N'))
$server = $null
$serverLogs = $null
$stage = '建立獨立暫存目錄'
$result = 1

function Assert-Check([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}

function New-Child([string]$File, [string[]]$Arguments) {
    $info = [Diagnostics.ProcessStartInfo]::new()
    $info.FileName = $File
    $info.WorkingDirectory = $repo
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    $info.StandardOutputEncoding = [Text.Encoding]::UTF8
    $info.StandardErrorEncoding = [Text.Encoding]::UTF8
    foreach ($argument in $Arguments) { $info.ArgumentList.Add($argument) }
    $child = [Diagnostics.Process]::new()
    $child.StartInfo = $info
    try { [void]$child.Start() } catch { $child.Dispose(); throw }
    return $child
}

function Invoke-Child([string]$File, [string[]]$Arguments) {
    $child = New-Child $File $Arguments
    try {
        $stdout = $child.StandardOutput.ReadToEndAsync()
        $stderr = $child.StandardError.ReadToEndAsync()
        Assert-Check ($child.WaitForExit(180000)) '子程序逾時'
        Assert-Check ($child.ExitCode -eq 0) '子程序失敗'
        # Capture secrets and diagnostics; never print raw child output.
        [void]$stderr.GetAwaiter().GetResult()
        return $stdout.GetAwaiter().GetResult()
    } finally {
        if (-not $child.HasExited) { $child.Kill($true); $child.WaitForExit() }
        $child.Dispose()
    }
}

function Start-Hub {
    $script:server = New-Child $exe @('-addr', '127.0.0.1:0', '-db', $db)
    $script:serverOutput = $server.StandardOutput.ReadToEndAsync()
    $line = $server.StandardError.ReadLineAsync()
    Assert-Check ($line.Wait(15000)) '啟動逾時'
    $log = $line.GetAwaiter().GetResult() | ConvertFrom-Json
    Assert-Check ($log.msg -eq 'HTTP server started') '服務未啟動'
    Assert-Check ($log.addr -match '^127\.0\.0\.1:[0-9]+$') '非預期監聽位址'
    $script:serverLogs = $server.StandardError.ReadToEndAsync()
    $script:origin = 'http://' + $log.addr
    $health = Invoke-WebRequest "$origin/healthz" -TimeoutSec 10 -NoProxy
    Assert-Check ($health.StatusCode -eq 200) '健康檢查失敗'
    Assert-Check (($health.Content | ConvertFrom-Json).status -eq 'ok') '健康回應不正確'
}

function Stop-Hub {
    if ($null -eq $script:server) { return }
    try {
        if (-not $server.HasExited) { $server.Kill($true) }
        Assert-Check ($server.WaitForExit(10000)) '程序未結束'
        if ($null -ne $script:serverLogs) {
            $logs = $serverLogs.GetAwaiter().GetResult()
            Assert-Check (-not $logs.Contains($token)) '日誌包含 Token'
        }
    } finally {
        $server.Dispose()
        $script:server = $null
        $script:serverLogs = $null
    }
}

function Call-API([string]$Method, [string]$Path, [int]$Expected, $Body = $null, [string]$ETag = '') {
    $headers = @{ Authorization = "Bearer $token" }
    if ($ETag) { $headers['If-Match'] = $ETag }
    $params = @{
        Uri = "$origin/api/v1/knowledge$Path"
        Method = $Method
        Headers = $headers
        TimeoutSec = 10
        NoProxy = $true
        SkipHttpErrorCheck = $true
    }
    if ($null -ne $Body) {
        $params.ContentType = 'application/json; charset=utf-8'
        $params.Body = [Text.Encoding]::UTF8.GetBytes(($Body | ConvertTo-Json -Compress))
    }
    $response = Invoke-WebRequest @params
    Assert-Check ($response.StatusCode -eq $Expected) 'HTTP 狀態不符'
    return $response
}

try {
    [void][IO.Directory]::CreateDirectory($work)
    $exe = Join-Path $work $(if ($IsWindows) { 'knowledge-hub.exe' } else { 'knowledge-hub' })
    $db = Join-Path $work 'memory.db'
    $stage = '建置目前原始碼'
    [void](Invoke-Child 'go' @('build', '-o', $exe, './cmd/knowledge-hub'))
    Write-Output "PASS $stage"

    $stage = '核發測試 Token'
    $issued = Invoke-Child $exe @('token', 'issue', '-db', $db, '-name', 'memory-smoke', '-project', 'memory-smoke') | ConvertFrom-Json
    $token = $issued.token
    Assert-Check (-not [string]::IsNullOrWhiteSpace($token)) '沒有 Token'
    Write-Output "PASS $stage"

    $stage = '啟動真實 HTTP 服務'
    Start-Hub
    Write-Output "PASS $stage"

    $stage = '寫入草稿並核對內容'
    $content = '部署識別詞是藍鯨號，部署前必須執行測試。'
    $body = @{ title = '記憶驗收'; content = $content; type = 'convention'; scope = 'project'; project = 'memory-smoke'; source = '自動驗收資料' }
    $created = Call-API 'POST' '' 201 $body
    $record = ($created.Content | ConvertFrom-Json).data
    $id = $record.id
    Assert-Check ($record.status -eq 'draft' -and $record.content -eq $content -and $record.version -eq 1) '草稿內容不符'
    Assert-Check ($null -eq $record.confirmed_by -and -not $record.usable_as_rule) '草稿確認狀態不符'
    Write-Output "PASS $stage"

    $stage = '預設搜尋排除草稿'
    $query = @{ query = '藍鯨號'; project = 'memory-smoke' }
    $found = (Call-API 'POST' '/search' 200 $query).Content | ConvertFrom-Json
    Assert-Check (@($found.data).Count -eq 0) '草稿出現在預設搜尋'
    Write-Output "PASS $stage"

    $stage = '確認並記錄確認者'
    $confirmed = Call-API 'PATCH' "/$id" 200 @{ status = 'confirmed' } ([string]$created.Headers.ETag[0])
    $record = ($confirmed.Content | ConvertFrom-Json).data
    $etag = [string]$confirmed.Headers.ETag[0]
    $confirmedAt = $record.confirmed_at
    Assert-Check ($record.status -eq 'confirmed' -and $record.version -eq 2 -and $record.confirmed_by -eq 'memory-smoke' -and $null -ne $confirmedAt -and $record.usable_as_rule) '確認欄位不符'
    Write-Output "PASS $stage"

    $stage = '搜尋命中正確記憶'
    $found = (Call-API 'POST' '/search' 200 $query).Content | ConvertFrom-Json
    Assert-Check (@($found.data).Count -eq 1 -and $found.data[0].id -eq $id) '搜尋 ID 不符'
    Write-Output "PASS $stage"

    $stage = '舊 ETag 更新遭拒絕'
    $conflict = (Call-API 'PATCH' "/$id" 412 @{ content = '不得覆蓋' } '"1"').Content | ConvertFrom-Json
    Assert-Check ($conflict.error.code -eq 'version_conflict') '衝突錯誤碼不符'
    Write-Output "PASS $stage"

    $stage = '強制停止後重啟，資料與 Token 保留'
    Stop-Hub
    Start-Hub
    $read = Call-API 'GET' "/$id" 200
    $record = ($read.Content | ConvertFrom-Json).data
    Assert-Check ($record.id -eq $id -and $record.content -eq $content -and $record.version -eq 2 -and $record.status -eq 'confirmed') '重啟後資料不符'
    Assert-Check ($record.confirmed_by -eq 'memory-smoke' -and $record.confirmed_at -eq $confirmedAt -and [string]$read.Headers.ETag[0] -eq $etag) '重啟後確認資訊不符'
    $found = (Call-API 'POST' '/search' 200 $query).Content | ConvertFrom-Json
    Assert-Check (@($found.data).Count -eq 1 -and $found.data[0].id -eq $id) '重啟後索引不符'
    Write-Output "PASS $stage"

    $stage = '封存後讀取及搜尋隱藏記憶'
    [void](Call-API 'DELETE' "/$id" 204 $null $etag)
    $missing = (Call-API 'GET' "/$id" 404).Content | ConvertFrom-Json
    Assert-Check ($missing.error.code -eq 'not_found') '封存後錯誤碼不符'
    $found = (Call-API 'POST' '/search' 200 $query).Content | ConvertFrom-Json
    Assert-Check (@($found.data).Count -eq 0) '封存資料出現在搜尋'
    Write-Output "PASS $stage"
    $result = 0
} catch {
    # Do not expose exception payloads, HTTP bodies, or credentials.
    Write-Output "FAIL $stage"
    $result = 1
} finally {
    try {
        Stop-Hub
        # Delete only the unique direct child of the system temp directory created above.
        $resolved = [IO.Path]::GetFullPath($work)
        Assert-Check ([IO.Path]::GetDirectoryName($resolved).TrimEnd('\', '/') -eq $tempRoot.TrimEnd('\', '/')) '暫存清理範圍不符'
        Assert-Check ([IO.Path]::GetFileName($resolved) -match '^akh-memory-smoke-[0-9a-f]{32}$') '暫存目錄名稱不符'
        if (Test-Path -LiteralPath $resolved) { Remove-Item -LiteralPath $resolved -Recurse -Force }
        Write-Output 'PASS 停止測試程序並清理暫存資料'
    } catch {
        Write-Output 'FAIL 停止測試程序或清理暫存資料'
        $result = 1
    }
}
if ($result -eq 0) { Write-Output 'PASS 記憶功能驗收完成' }
exit $result
