# 記憶功能 Review Report

日期：2026-11-07。環境：Windows、PowerShell 7.6.9、Go 1.27.3 windows/amd64。
範圍：閱讀程式與既有測試、執行驗證，後續依授權修正 Windows 測試路徑；未修改產品程式、未進入新 Milestone。

## Windows 路徑修正後驗證

後續已新增 `scripts/memory-smoke.ps1`。Windows 實際執行成功：建置、Token 核發、HTTP 啟動、寫入草稿、預設搜尋排除草稿、確認者、中文搜尋、舊 ETag 拒絕、強制重啟後同一 Token 讀取相同內容／版本／確認者並搜尋命中、封存後 GET 404 與搜尋空結果、程序及暫存清理均 PASS，退出碼 0。

另外在獨立子程序清空 PATH，模擬找不到 Go：腳本顯示建置階段 FAIL、退出碼 1，暫存目錄無殘留。已修正重導向時的中文輸出編碼，並核對 UTF-8 擷取結果。最終腳本再跑一次正常流程，全部 PASS。新增腳本後 build、vet、全套 test 與 race 也再次通過。

這補齊實際執行檔的程序中止恢復驗收；仍未驗證 Windows Ctrl+C 正常關機、斷電耐久性或 Agent 跨對話記憶。以下「修正前驗證紀錄」與手動流程保留為歷史及人工操作參考。

同日先以 `go test -count=1 ./...` 重現原始失敗，再修改測試 fixture；沒有刪除或跳過測試。修正後：

- `go test -count=1 ./...`：全套通過，token 套件無測試。
- `go test -race -count=1 ./...`：全套通過，無 race 報告，token 套件無測試。
- `go build ./...`、`go vet ./...`：通過。
- `gofmt -l internal/sqlite/db_test.go`：無輸出；僅格式化本次修改的 Go 檔案。
- `git diff --check` 與新增報告的 `git diff --no-index --check`：無空白錯誤。no-index 對新增檔案回傳差異狀態 1；LF/CRLF 提示不是空白錯誤。

未在 macOS／Linux 重跑；其他平台保留原測試路徑。此次修正不改變 API、資料庫 Schema 或記憶行為。

## 結論與待補項目

目前是可持久化的知識 REST API：HTTP → Service → Repository → SQLite。包含 Token、Project 白名單、CRUD、確認者、ETag 與 FTS5。尚無 MCP、Embedding、Web UI 或自動擷取／載入 Agent 記憶。搜尋是文字匹配，不能用同義問句召回能力作為現有功能的驗收標準。

1. **Windows 測試阻塞已修正**：原測試使用 `folder ?#`，在建立目錄時失敗，尚未進入 SQLite。後續依使用者授權，Windows 改用合法的 `folder #`，其他平台保留原路徑；保留特殊字元與 PRAGMA 測試，不跳過案例。產品程式未修改。
2. **操作文件需整理**：README 第 51 行仍稱未建立確認欄位；API 文件第 51、191 行仍稱 project 不是授權隔離，與現有白名單契約衝突。architecture 明確保留 M1 歷史段落，閱讀時需以最新覆蓋內容為準。
3. **實際 Agent 串接待驗證**：資料庫測試成功不等於新對話會自動記得。需要呼叫端明確搜尋／寫入，並使用相同 DB 與正確 Token/project。
4. **既定限制**：草稿預設不出現在列表／搜尋；每個搜尋詞至少三字；修改 confirmed 內容會降回 draft；沒有去重／POST 冪等鍵、修訂歷史、封存恢復、角色分離。這些是現行範圍，不一律視為 Bug。
5. **格式差異**：`gofmt -l .` 列出 27 個 Go 檔案；抽查 service.go 的 Git index 是 LF、工作目錄是 CRLF。未進行整批格式重寫。

## 修正前驗證紀錄

| 命令 | 實際結果 |
| --- | --- |
| `go build ./...` | 通過 |
| `go vet ./...` | 通過 |
| `go test -count=1 ./...` | 失敗：TestOpenInitializesSQLite 的 Windows 非法路徑；cmd、config、httpapi、knowledge 套件通過，token 無測試 |
| `go test -count=1 -v ./internal/sqlite -run 'TestKnowledgeMigrationPreservesDataAcrossRestart\|TestSearchIndexTracksCreateUpdateAndArchive\|TestStoppedDatabaseBackupCanBeRestored'` | 三項皆通過 |
| `go test -race ./...` | 失敗：同一 Windows 非法路徑；其餘已執行項目未出現 race 報告，不宣稱全套通過 |
| `gofmt -l .` | 非空；27 個檔案，存在工作目錄 CRLF 差異 |
| `git diff --check` | 通過；新增報告另以 no-index --check 檢查空白 |

既有 HTTP 測試包含 CRUD 狀態轉移、確認者、專案隔離、搜尋、ETag 衝突、Token、loopback 與併發。持久化定向測試是關閉／重開 SQLite，不是正式執行檔重啟驗收。本輪未做實際 Agent 新對話、正式執行檔啟停或 Linux 驗證。

## 手動實測（PowerShell，使用獨立 DB）

在專案根目錄開啟終端 A，核發測試 Token 後啟動服務。這會建立獨立資料庫，不使用預設 knowledge.db；若 18080 已被使用，換另一個 loopback 埠。

```powershell
$issued = go run ./cmd/knowledge-hub token issue -db data/memory-smoke.db -name memory-review -project memory-smoke | ConvertFrom-Json
$issued.token
go run ./cmd/knowledge-hub -addr 127.0.0.1:18080 -db data/memory-smoke.db
```

在終端 B 執行；把剛核發的 Token 輸入提示欄位，不貼進版本控制文件。

```powershell
$base = 'http://127.0.0.1:18080/api/v1/knowledge'
$token = Read-Host '輸入測試 Token' -MaskInput
$headers = @{ Authorization = "Bearer $token" }
$body = @{
    title = '記憶測試規則'
    content = '測試專案的識別詞是藍鯨號，部署前必須執行測試。'
    type = 'convention'
    scope = 'project'
    project = 'memory-smoke'
    source = '人工測試資料'
} | ConvertTo-Json
$created = Invoke-RestMethod $base -Method Post -Headers $headers -ContentType 'application/json; charset=utf-8' -Body $body
$id = $created.data.id
$created.data # 預期 draft、version=1、confirmed_by=null
$query = @{ query = '藍鯨號'; project = 'memory-smoke' } | ConvertTo-Json
Invoke-RestMethod "$base/search" -Method Post -Headers $headers -ContentType 'application/json; charset=utf-8' -Body $query
# 預期 data 為空：尚未確認
$headers['If-Match'] = '"1"'
Invoke-RestMethod "$base/$id" -Method Patch -Headers $headers -ContentType 'application/json' -Body '{"status":"confirmed"}'
# 預期 version=2、confirmed_by=memory-review、confirmed_at 有值
Invoke-RestMethod "$base/search" -Method Post -Headers $headers -ContentType 'application/json; charset=utf-8' -Body $query
# 預期搜尋命中剛建立的 ID
```

接著在終端 A 按 Ctrl+C，等待結束，再用相同 `-db` 與 `-addr` 啟動。保留終端 B 的變數，執行：

```powershell
Invoke-RestMethod "$base/$id" -Headers $headers
Invoke-RestMethod "$base/search" -Method Post -Headers $headers -ContentType 'application/json; charset=utf-8' -Body $query
# 預期相同 ID、內容、version=2 與確認者仍在，搜尋仍命中
$headers['If-Match'] = '"2"'
Invoke-RestMethod "$base/$id" -Method Delete -Headers $headers
Invoke-RestMethod "$base/search" -Method Post -Headers $headers -ContentType 'application/json; charset=utf-8' -Body $query
# 預期封存後搜尋不再命中此 ID；GET 此 ID 應回 404
```

上述是供使用者執行的手動流程，本輪未宣稱已執行。重複執行會新增不同 ID；請以本次 `$id` 比對結果。測試資料庫保留供檢查，DELETE 只是封存。

下一階段前置條件：完成上述正式程序重啟驗收；若目標是 Agent 跨對話記憶，需另定呼叫端串接與召回案例。
