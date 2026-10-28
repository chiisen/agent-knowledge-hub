# agent-knowledge-hub 專案規劃、架構設計與實作計畫

> 狀態：Milestone 1 已依使用者最新範圍完成本機實作與驗證，等待 Code Review；其餘規劃仍待確認。
> 整理日期：2026-10-28。
> 來源：本次對話的專案需求與前一輪架構建議。
> 文件建立不代表架構選項已獲確認。第 10 節列出必須確認的需求。
> 執行方式：依 Milestone 逐階段實作、測試及人工 Code Review；未經授權不得跨階段。
> AI Agent 執行時使用適用的 executing-plans 流程；本文件不授權自動 commit、push 或開始下一階段。

### 最新執行範圍覆蓋（2026-10-28）

使用者最新指示將 Milestone 1 擴為：Go Module、HTTP Server、GET /healthz、SQLite Driver／FTS5 能力確認、knowledge 主表、版本化 Migration、Foreign Keys／WAL／Busy Timeout、slog、Graceful Shutdown、loopback 預設、單元／初始化整合測試及 README／AGENTS 更新。

此指示優先於第 9 節原有 M1／M2 邊界；原 M2 的初始化與 Migration 工作移入 M1，Repository CRUD 與正式 FTS 索引仍留後續。M1 不含 Knowledge CRUD、搜尋 API、MCP、Embedding、Web UI 或完整 Token 系統。原先待決策不因此全部轉為已確認。

目前狀態：M1 的 build／test／vet／race 與實際執行檔啟停驗證通過，停止等待人工 Code Review，不執行 M2。

已確認 Schema：id TEXT 主鍵且非 NULL；title／content／type／scope 為非空 TEXT，type／scope 不限定詞彙；project／module 可 NULL；status 預設 draft 並限 draft／confirmed／deprecated；source 為 TEXT，不強制 JSON；時間為 UTC Unix milliseconds。version、archived_at、確認資訊、Evidence／revisions 留後續新 Migration。此決策優先於第 5 節擴充草案；SQL 不負責 UUID 格式驗證或自動產生時間。

**目標**：建立輕量、可自行部署的 Knowledge API Server，讓不同 AI Coding Agent 共用可追溯的長期知識。

**架構**：HTTP Handler → Knowledge Service → Repository → SQLite。HTTP、業務規則與資料庫實作分離；僅在必要邊界定義介面，不建立通用資料存取框架。

**技術方向**：Go、net/http、database/sql、SQLite FTS5、log/slog；優先單一執行檔，支援 macOS、Windows、Linux。

## 全域約束與執行規則

- 使用仍受支援的 Go 穩定版本；實作時查核並鎖定版本。
- 不使用 Docker、Redis、PostgreSQL 常駐服務、本機 LLM 或大型 Framework。
- MVP 不包含 Web UI、MCP、Embedding、Vector Search、AI 推論或背景排程。
- 遵守 KISS、YAGNI、微創異動；不進行無關重構。
- 不為通過測試刪除、略過或弱化測試；Bug 修正附 Regression Test。
- 需求或架構歧義會改變行為、範圍或驗收時，先詢問使用者。
- 修改前宣告本階段範圍與驗收；每項修改對應驗收項目。
- 各 Milestone 完成後執行相關測試、更新文件與 CHANGELOG，停止等待人工 Review。
- 交付報告包含完成功能、修改檔案、測試結果、已知問題與人工 Review 重點。
- 編譯成功、本機測試通過、跨平台實機驗證分開回報，不互相替代。
- 本文件中的數值與方案均為建議，除非另有決策紀錄，不得宣稱已獲確認。

## 1. 需求分析與可能的盲點

系統負責知識儲存、查詢、管理與 API 存取，不負責知識推論、真偽判斷或自動確認。

核心需求：

1. 保存任務完成後的重要經驗與決策。
2. 累積未文件化的專案規範與 Code Review 發現。
3. 讓不同 Agent 共用已確認知識。
4. 保留內容與來源證據的可追溯性。
5. 未來讓獨立 Processor 透過 API 進行探勘、清理與 RAG 處理。

### 1.1 主要盲點

| 議題 | 風險 | 建議方向 |
| --- | --- | --- |
| 確認權限 | Agent 自行確認會弱化 confirmed 的意義 | 區分一般寫入與確認權限 |
| confirmed 修改 | 舊審核可能被套用至新內容 | 實質異動降回 draft |
| deprecated 與封存 | 歷史參考與一般隱藏混淆 | 狀態與 archived_at 分開 |
| 個人／專案範圍 | 個人偏好誤作專案規範 | 明示 scope，不自動混合 |
| 中文搜尋 | 預設 tokenizer 不等於中文斷詞 | 先定義代表性搜尋案例 |
| 修訂追溯 | 現況來源無法還原歷史版本 | 決定是否第一版保留 revisions |
| 並行修改 | 後寫覆蓋前寫 | 版本條件更新 |
| 新增重試 | 回應遺失後重送造成重複 | 決定是否需要持久化冪等機制 |
| SSD 容量 | 封存仍占空間 | 定義保存與清除政策 |
| Processor 同步 | 一般分頁無法保證增量不漏資料 | 不先承諾可靠變更串流 |

confirmed 表示經指定流程確認，不表示永遠正確，也不賦予知識內容高於使用者指示的權限。消費端必須把知識當資料，避免將內容中的指令視為授權。

## 2. MVP 功能邊界

### 2.1 納入

- 指定的七個 REST API。
- 知識新增、讀取、更新、封存。
- project、module、type、status 篩選。
- personal／project 範圍區分。
- FTS5 搜尋、分頁、輸入與結果數量限制。
- draft／confirmed／deprecated 生命週期。
- 結構化來源、確認資訊與版本條件。
- Token 驗證、安全監聽設定、JSON 錯誤。
- Schema Migration、Context、Graceful Shutdown、Structured Logging。
- 本機測試、基本 Benchmark、備份還原及手動維護文件。

### 2.2 不納入

- MCP、Embedding、Vector Search、RAG 推論。
- Processor、知識自動確認、自動合併衝突與背景工作。
- Web UI、完整帳號系統與完整 RBAC。
- 附件上傳、來源 URI 自動抓取。
- 知識關聯圖、標籤系統。
- 公網代管、高可用與多程序共同寫入的部署設計。

建議使用模型為「單一擁有者、多個受信任 Agent」。是否需要跨使用者或跨專案安全隔離，仍待決策；範圍篩選不等於授權隔離。

## 3. 技術選型與分層

| 項目 | 建議 | 理由與限制 |
| --- | --- | --- |
| Go | 仍受支援的穩定系列最新修補版 | 前輪查核為 1.27.3；開工時重新確認 |
| HTTP | net/http、ServeMux | 路由少，不需要 Framework |
| JSON | encoding/json | 標準庫足夠 |
| Database API | database/sql | Context、交易與連線池 |
| SQLite driver | 優先 modernc.org/sqlite | 無 CGO，跨平台建置較單純；資源成本需實測 |
| Migration | embed.FS、版本化 SQL、小型 runner | 不建立通用 Migration 平台 |
| UUID | 成熟的小型 UUID 套件，UUID v4 | 避免自行實作；具體套件待鎖定 |
| Logging | log/slog JSON handler | 標準庫與結構化輸出 |
| 設定 | flags、環境變數 | 不先引入 YAML Framework |
| 測試 | testing、httptest、暫存 SQLite 檔案 | 不依賴外部服務 |

備選驅動 mattn/go-sqlite3 走原生 SQLite 路線，但需要 CGO／C 工具鏈。modernc 不保證更快或更省 RAM，需用本專案資料驗證。

驅動內嵌的 SQLite 必須包含 WAL-reset 修正；官方列出 3.51.3 起修正，部分舊系列有回補。檢查內嵌版本，不以作業系統 sqlite3 版本代替。

### 3.1 分層責任

| 層 | 責任 |
| --- | --- |
| HTTP Handler | HTTP、JSON、條件標頭、驗證身分與錯誤映射 |
| Knowledge Service | 輸入規則、範圍、狀態轉移、確認權限 |
| Repository interface | 業務所需的持久化操作契約 |
| SQLite implementation | SQL、交易、FTS、Migration、驅動錯誤轉換 |

Repository interface 由 Service 使用端定義，不暴露 sql.Rows、SQLite rowid 或 FTS 資料結構。不建立通用 ORM、通用 Repository、多資料庫工廠或 DI container。

替換 PostgreSQL 時更換 Repository、Migration 與搜尋實作；搜尋語義和排名仍須重新驗證。

## 4. 預計專案目錄

```text
agent-knowledge-hub/
├── cmd/knowledge-hub/main.go
├── internal/
│   ├── config/
│   ├── knowledge/
│   │   ├── model.go
│   │   ├── service.go
│   │   ├── repository.go
│   │   └── errors.go
│   ├── httpapi/
│   │   ├── router.go
│   │   ├── handler.go
│   │   ├── middleware.go
│   │   └── response.go
│   └── sqlite/
│       ├── db.go
│       ├── repository.go
│       ├── migrate.go
│       └── migrations/001_initial.sql
├── docs/
│   ├── architecture.md
│   ├── api.md
│   ├── implementation-plan.md
│   └── decisions/
├── README.md
├── AGENTS.md
├── CHANGELOG.md
├── go.mod
└── go.sum
```

檔案依所屬 Milestone 建立，不先產生所有空檔。測試放對應套件旁；僅真正共用的資料放 testdata/。

SQLite、WAL、SHM 與執行期資料置於本機資料目錄，不加入 Git。單一執行檔不表示執行時只有一個檔案。

## 5. SQLite Schema 與資源設計

### 5.1 knowledge 後續擴充草案（非 M1 Schema）

本表保留前輪候選設計供後續討論，不是目前 001_initial.sql。M1 實際 Schema 以上方已確認最小版本及 docs/architecture.md 為準。

| 欄位 | SQLite 型別 | 規則 |
| --- | --- | --- |
| row_id | INTEGER PRIMARY KEY | 內部主鍵，供 FTS rowid 使用 |
| id | TEXT UNIQUE NOT NULL | 對外 UUID v4 |
| title | TEXT NOT NULL | 非空、有長度限制 |
| content | TEXT NOT NULL | 非空、有位元組限制 |
| type | TEXT NOT NULL | 詞彙待決策 |
| scope | TEXT NOT NULL | personal／project |
| project | TEXT NULL | project scope 必填；personal 為 NULL |
| module | TEXT NULL | 建議只允許 project scope |
| status | TEXT NOT NULL | draft／confirmed／deprecated |
| source_json | TEXT NOT NULL | 結構化來源物件 |
| version | INTEGER NOT NULL | 初始 1，異動遞增 |
| created_at | INTEGER NOT NULL | UTC Unix milliseconds |
| updated_at | INTEGER NOT NULL | UTC Unix milliseconds |
| archived_at | INTEGER NULL | 封存時間 |
| confirmed_at | INTEGER NULL | 當前版本確認時間 |
| confirmed_by | TEXT NULL | 伺服器驗證身分 |

API 時間以 UTC RFC 3339 表示。資料庫 CHECK 覆蓋 enum、scope 組合、正版本、合法 JSON 物件及確認欄位一致性。完整長度、來源結構與狀態驗證由 Service 負責。

### 5.2 Evidence 評估

建議 MVP 不拆 Evidence 表。目前沒有證據獨立 CRUD、共用、搜尋或附件管理需求。

source 示意：

```json
{
  "agent": "codex",
  "evidence": [
    {
      "kind": "git",
      "uri": "https://github.com/example/repo",
      "revision": "完整 commit SHA",
      "path": "internal/example.go",
      "locator": "L40-L68",
      "excerpt": "有限長度的必要片段"
    }
  ]
}
```

- 優先保存穩定 URI、revision、定位與必要摘錄。
- 不保存整份聊天、log 或大型附件。
- 不自動抓取 URI；來源紀錄不等於已驗證內容。
- confirmed_by 由伺服器身分產生，不能由請求自行宣告。
- 若需歷史追溯，評估 Knowledge revisions；單獨拆 Evidence 無法保留歷次修改。

### 5.3 一般索引與 FTS5

初始索引建議：

- UUID 唯一索引。
- 未封存資料的 `(scope, project, status, created_at, id)`。
- 未封存資料的 `(scope, project, module)`。
- type 索引依 EXPLAIN QUERY PLAN 與 Benchmark 再決定。

FTS5 使用 external-content，對應 knowledge.row_id，索引 title、content，避免額外保存完整內容副本。

由 INSERT／UPDATE／DELETE trigger 維護，與主資料同交易提交。搜尋 JOIN 主表並套用 scope、status、archived_at 條件。封存資料初期可保留索引，但不得出現在一般結果。

tokenizer 尚未定案：unicode61 不提供中文詞彙斷詞；trigram 支援子字串，但 MATCH 不匹配少於三個 Unicode 字元的子字串。不得未經確認承諾兩字中文搜尋。

提供手動 rebuild、integrity-check 與維護文件，不加入背景索引工作。

### 5.4 連線與交易

| 項目 | 起始建議 |
| --- | --- |
| 寫入池 | 1 個連線 |
| 讀取池 | 最多 2 個連線 |
| foreign_keys | 每個實體連線啟用 |
| journal_mode | WAL，啟動設定並讀回確認 |
| busy_timeout | 2 秒 |
| synchronous | FULL |
| page cache | 每連線約 2 MiB |
| wal_autocheckpoint | 1,000 pages |
| mmap | 初期不主動擴大 |

連線級 PRAGMA 必須套用每個實體連線，不能只對 sql.DB 執行一次便假定全池生效。固定 PRAGMA 與 Migration 由受信任程式定義；所有外部資料使用參數綁定，不拼接輸入。動態欄位與排序僅使用白名單。

交易流程：先驗證，再開短交易，以版本條件更新，連同來源、確認資訊與 FTS 原子提交，Commit 成功才回應。不在交易內執行網路請求，不做無限制鎖重試。

### 5.5 WAL、Migration 與空間

- 關閉 Rows、避免長讀交易阻礙 checkpoint。
- 使用 auto checkpoint，不新增排程。
- 正常關機排空讀取後，可有期限地嘗試 checkpoint。
- journal_size_limit 不是活躍 WAL 的硬上限。
- VACUUM、FTS optimize／rebuild 採手動維護。
- 使用本機磁碟，不把運作中資料庫放入網路共享或雲端同步目錄。
- Migration 在開始監聽前執行；記錄版本與 checksum，失敗停止啟動。
- 先採向前 Migration；還原依經驗證的備份，不假設自動降版安全。
- MVP 先提供正常停機備份與還原流程；運作中不能只複製 .db 就宣稱完整備份。

## 6. REST API 規格草案

### 6.1 共通契約

- request／response 使用 JSON。
- Token 使用 Authorization: Bearer，不放 URL。
- 每筆資料有 version；單筆回應附 ETag。
- PATCH／DELETE 必須有 If-Match。
- scope／project 建議建立後不可修改。
- 封存後不得透過一般 PATCH 修改或恢復。
- 列表與搜尋預設只回未封存 confirmed。
- 單筆 GET 可讀 draft／deprecated，但明示 status 與衍生的 usable_as_rule。
- usable_as_rule 不能由客戶端寫入，也不是完整安全保證。

範圍建議：新增／搜尋在 body 指定 scope、project；其他端點在 query 指定。project scope 必填 project，personal 不自動混入專案資料。單筆 ID 仍檢查範圍。

### 6.2 端點

| 方法與路徑 | 成功 | 語義 |
| --- | --- | --- |
| POST /api/v1/knowledge | 201、Location | 伺服器產生 UUID、建立 draft |
| GET /api/v1/knowledge/{id} | 200、ETag | 取得指定範圍的單筆 |
| GET /api/v1/knowledge | 200 | 篩選、分頁 |
| PATCH /api/v1/knowledge/{id} | 200、新 ETag | 部分更新與狀態轉移 |
| DELETE /api/v1/knowledge/{id} | 204 | 設定 archived_at |
| POST /api/v1/knowledge/search | 200 | FTS 搜尋與篩選 |
| GET /healthz | 200／503 | 有期限的 DB 基本可用性檢查 |

重複封存已封存資料建議回 204；不存在或範圍不符回 404。封存與 If-Match 的優先處理順序，需在 API 詳細設計時明確定義並測試。

### 6.3 新增、更新與確認

新增必要資料為 title、content、type、scope、適用時的 project、source；module 可省略。status 預設 draft。

PATCH：

- 未提供欄位維持原值；null 只能清除可空欄位。
- 拒絕空更新、未知欄位與不可修改欄位。
- confirmed 的標題、內容、類型或來源異動後降為 draft，清除當前確認資訊。
- 一般 Agent 不能修改後同時自行 confirmed。
- 建議確認前至少有一項符合來源契約的證據；人工決策是否算證據待確認。
- 完整狀態轉移矩陣，包含 deprecated 是否能重新確認，須於實作前定案。

### 6.4 分頁與輸入限制

| 項目 | 建議值 |
| --- | --- |
| limit | 預設 20，最大 100 |
| offset | 最大 10,000 |
| 一般排序 | created_at DESC、id DESC |
| 搜尋排序 | relevance、UUID 同分排序 |
| 搜尋長度 | 最多 256 Unicode 字元 |
| content | 最大 64 KiB |
| request body | 最大 128 KiB |
| 每筆 evidence | 最多 10 項 |

使用 limit + 1 判斷 has_more，不預設全量 COUNT。列表回摘要，單筆 GET 回全文。title、source 子欄位及 excerpt 的精確上限於相關 Milestone 開始前確認。

offset 分頁在並行新增／更新時不保證快照一致性，不能用作可靠同步契約。

### 6.5 搜尋

搜尋支援 project、module、type、status 與 scope 篩選。初期建議一般文字、各搜尋項 AND，不直接開放原始 FTS 語法。

由程式轉換成受控 FTS 查詢，再用參數綁定。SQL 參數化不會自動消除 FTS 語法解讀，兩者都需驗證。

### 6.6 Token 與監聽

建議第一版實作 Token，而非只預留介面：

- 從環境變數或受保護檔案載入；無內建預設值。
- 缺少 Token 時拒絕啟動知識 API。
- 為 Token 配置名稱，供確認身分使用。
- 最小能力建議 agent／maintainer；一般寫入與確認分開。
- Token 的專案限制、修改 confirmed 與封存權限仍待決策。
- 預設只監聽 127.0.0.1。
- 非 loopback 需明確啟用遠端存取；空值不能退回 0.0.0.0 或 ::。
- 遠端使用 HTTPS 或加密隧道，不以明文 HTTP 傳 Token。
- healthz 可免 Token，只回最少狀態。

## 7. 錯誤處理與可觀測性

錯誤格式：

```json
{
  "error": {
    "code": "version_conflict",
    "message": "資料已被其他請求更新，請重新讀取。",
    "request_id": "伺服器產生的識別碼"
  }
}
```

| HTTP | 情境 |
| --- | --- |
| 400 | JSON、UUID、query 或搜尋語法錯誤 |
| 401 | Token 缺失或錯誤 |
| 403 | 操作權限不足 |
| 404 | 不存在、範圍不符、一般讀取的封存資料 |
| 405 | 方法不支援 |
| 412 | If-Match 版本不符 |
| 413 | Body 過大 |
| 415 | Content-Type 不支援 |
| 422 | 欄位值或狀態轉移不合法 |
| 428 | 缺少必要 If-Match |
| 500 | 未預期內部錯誤 |
| 503 | DB busy 或服務端處理期限耗盡 |

Repository 將驅動錯誤轉成應用錯誤；不對外回傳 SQL、內部路徑或驅動原始訊息。客戶端已斷線的取消不強行寫入 JSON 回應。

日誌記錄 request ID、route template、method、status、duration、operation、穩定 error code；必要時記錄 knowledge ID、version、驗證身分名稱。

禁止記錄 Token、Authorization、request／response body、title、content、來源摘錄、搜尋字串或完整 query string。一般請求一筆完成日誌，避免跨層重複記錄錯誤。

輸出 stdout／stderr，不自行建立無限制 log 檔。文件提供作業系統服務與輪替方式；重導向仍需容量管理。

| Timeout | 建議值 |
| --- | --- |
| ReadHeaderTimeout | 5 秒 |
| Body read／ReadTimeout | 10 秒 |
| 應用程式 deadline | 5 秒 |
| WriteTimeout | 10 秒 |
| IdleTimeout | 60 秒 |
| Graceful Shutdown | 10 秒 |

Context 從 HTTP 傳至 Service 與 QueryContext／ExecContext／BeginTx。Busy timeout 不等於查詢 timeout，驅動取消行為必須測試。

關機：停止接收新請求 → 排空進行中請求 → 逾時取消剩餘工作 → 有期限 checkpoint → 關閉 DB。使用 http.Server.Shutdown，不直接跳過排空。

## 8. 測試策略與 Review Focus

### 8.1 測試矩陣

| 類別 | 驗收內容 |
| --- | --- |
| Unit | 欄位、scope、確認權限、狀態轉移 |
| SQLite integration | Migration、約束、FTS trigger、rollback、持久化 |
| HTTP | JSON、Token、錯誤、分頁、ETag、大小限制 |
| Concurrent read/write | 多 Agent 寫入、讀寫並行、版本競爭、索引一致 |
| Timeout | 鎖等待、取消、期限、shutdown |
| Logging | request ID 與敏感資料不外洩 |
| Benchmark | 新增、單筆、篩選、搜尋、混合負載 |

SQLite integration 使用 t.TempDir() 的實體檔案，不只依賴 :memory: 驗證 WAL／多連線。

### 8.2 必要案例

- 兩個請求用同版本更新，只有一個成功。
- confirmed 修改後降回 draft。
- 一般列表／搜尋排除 draft、deprecated、封存。
- personal 不混入 project；單筆 UUID 也不能繞過範圍。
- 主資料、source、確認資訊與 FTS 原子提交／回滾。
- 外部連線持寫鎖時在期限內回應。
- 提交後重啟仍能讀取。
- 新建立的實體連線同樣具備 PRAGMA。
- 中英文代表案例符合選定 tokenizer 契約。
- JSON 錯誤、404、405 也符合統一回應格式。

### 8.3 人工 Review 特別注意

1. 取消或逾時與 Commit 同時發生：不把已提交誤報為已回滾，說明重試限制。於 Milestone 2、4、5 驗證。
2. 新連線或重啟後的 PRAGMA 漂移：不可僅測第一個連線。於 Milestone 2 驗證。
3. confirmed 內容／來源變更與權限繞過：同交易維護確認狀態。於 Milestone 3、4 驗證。
4. 中文短詞、標點與 FTS 控制語法：不得無聲承諾不支援的搜尋。於 Milestone 2、4 驗證。
5. 錯誤與 panic 路徑的敏感內容：日誌不得洩漏請求全文或 Token。於 Milestone 1、4、5 驗證。

### 8.4 驗證指令

在相關套件存在且環境具備工具後執行：

```sh
gofmt -l .
go vet ./...
go test ./...
go test -race ./...
go test -run '^$' -bench . -benchmem ./...
git diff --check
```

預期：gofmt 無輸出、vet／測試成功、Race 無報告、diff check 成功。Benchmark 僅在該階段有實際基準時執行，不把無測試可跑當通過。

Race Detector 可能需要 C 編譯器且增加資源使用，與正式執行檔無 CGO 建置需求分開說明。

Benchmark 使用固定 seed、代表性中英資料，先測 1,000／10,000 筆。記錄 latency、allocations、混合負載 p50／p95、RSS、DB／FTS／WAL 大小。未定義工作負載前不承諾數值 SLA。

## 9. 分階段實作與驗收

本節將前輪階段編為 Milestone，仍為待 Review 的劃分。使用者先前要求「只完成 Milestone 1 後停止」的執行邊界持續有效；本次只有文件整理授權。

### Milestone 0：決策收斂

- [ ] 確認第 10 節中影響第一階段的決策及 Milestone 邊界。
- [ ] 確認其餘決策的最晚定案階段，避免未確認 Schema 或 API 提前落地。
- [ ] 記錄已確認選擇；不得把本文件的建議自動當成同意。

驗收：實作者能明確指出當階段允許建立的功能、依賴與完成條件。

### Milestone 1：專案骨架、HTTP、SQLite 初始化與 Migration

檔案：go.mod、go.sum、cmd/knowledge-hub/main.go 與測試、internal/config/、internal/httpapi/、internal/sqlite/ 與測試、migrations/001_initial.sql；.gitignore、README.md、AGENTS.md、docs/architecture.md、CHANGELOG.md、任務狀態。

範圍：

- [x] 使用現有 Go 1.27.3，module path 依 Git remote；flags-only，預設 127.0.0.1:8080。
- [x] 建立最小可建置入口、設定解析與驗證，拒絕非 loopback。
- [x] 設定 slog、request ID、HTTP 逾時與 Graceful Shutdown。
- [x] 實作真實 DB healthz、SQLite／FTS5 能力確認與連線 PRAGMA。
- [x] 建立最小 knowledge 表與交易式版本／checksum Migration。
- [x] 驗證重複 Migration、重啟資料保留與失敗回滾。
- [x] 補齊本階段文件與 DB Git 排除規則。

測試步驟：先測設定與生命週期的預期失敗，再實作最小功能；覆蓋預設 loopback、無效設定、非 loopback 防護、關機排空與日誌敏感值保護。

驗收：可建置、可啟停、預設安全、相關 Unit／HTTP lifecycle tests 通過；執行 gofmt、vet、test、race 與 diff check。

不包含：Knowledge CRUD、正式 FTS 索引／搜尋 API、Token 系統。M1 只提供本機 healthz。

**完成即停止，交付檔案與測試證據，等待使用者 Code Review。**

### Milestone 2：SQLite、Migration 與 Repository

依最新範圍，SQLite 初始化、初版 Schema 與 Migration runner 已在 M1 完成，不重做；本階段只針對 Repository 及經確認的後續 Migration／FTS 工作規劃，開始前需使用者另行指定。

前置決策：driver、Schema、scope、type、Evidence／revisions、搜尋語義。

預計檔案：internal/knowledge/model.go、repository.go、errors.go、internal/sqlite/db.go、migrate.go、repository.go、migrations/001_initial.sql 與對應 integration tests；更新架構、決策與 CHANGELOG。

- [ ] 先寫 Migration、PRAGMA、CRUD、版本競爭與 rollback 失敗測試。
- [ ] 實作連線設定、Schema Migration 與 checksum。
- [ ] 實作新增、單筆、分頁、更新、封存的 Repository 操作。
- [ ] 實作 FTS 與 trigger；若 tokenizer 未定案，停止相關實作先確認。
- [ ] 驗證重啟持久化、多實體連線設定、FTS 一致性與鎖逾時。

驗收：實體 SQLite integration tests 通過，無外部服務；不宣稱已完成對外 HTTP CRUD。

### Milestone 3：Knowledge Service 與生命週期

前置決策：確認者、證據規則、狀態轉移、欄位限制及操作權限。

預計檔案：internal/knowledge/service.go、相關模型／錯誤與 Unit tests；更新架構、決策與 CHANGELOG。

- [ ] 先寫輸入、scope、確認權限與狀態轉移失敗測試。
- [ ] 實作驗證、確認／降回 draft、來源規則與範圍不可變條件。
- [ ] 經 Repository 原子執行版本條件更新，不暴露 SQLite 型別。

驗收：Unit tests 與既有 integration tests 通過；Service 不依賴 HTTP 或具體 SQLite driver。

### Milestone 4：REST API 與安全邊界

前置決策：Token／project 權限、If-Match、冪等新增需求、API 限制與遠端方案。

預計檔案：internal/httpapi/router.go、handler.go、middleware.go、response.go、HTTP tests；更新入口、docs/api.md、README 與 CHANGELOG。

- [ ] 先寫七端點、驗證、錯誤與授權失敗測試。
- [ ] 串接 Service、實作 CRUD、列表、搜尋與真實 DB healthz。
- [ ] 實作 JSON 限制、分頁、ETag／If-Match、request ID 與錯誤映射。
- [ ] 驗證 scope、封存、confirmed 預設與 Token／日誌邊界。

驗收：HTTP tests 與完整既有測試通過，API 文件可供 Agent 直接使用。

### Milestone 5：並行、故障與資源驗證

預計檔案：對應套件的並行／timeout tests、Benchmarks；必要的小範圍修正及文件。

- [ ] 驗證多 Agent 讀寫、同版本競爭、鎖、取消、關機與重啟。
- [ ] 執行 Race Detector，不以程式競態測試代替資料庫競爭測試。
- [ ] 建立 Benchmark 與 RSS／DB／FTS／WAL 基線。
- [ ] 只修正影響既定驗收的問題，不擴大重構。

驗收：故障情境可重現且結果正確；容量、效能數據與限制有明確測試條件。

### Milestone 6：文件、備份與跨平台發行準備

- [ ] 補齊安裝、Agent 存取、安全與手動維護文件。
- [ ] 驗證停機備份與還原。
- [ ] 建立 macOS／Windows／Linux 的建置與測試矩陣。
- [ ] 明列未實機驗證平台，不以 cross-compile 代替執行驗證。
- [ ] 確認授權、依賴與發行內容。

驗收：文件可重現啟動與還原；發行矩陣、已知限制與驗證證據完整。發佈需另行授權。

### 9.1 文件責任

| 文件 | 內容 |
| --- | --- |
| README.md | 用途、安裝、啟動、安全與範例 |
| AGENTS.md | 開發原則、Go 慣例、檢查與測試要求 |
| docs/architecture.md | 分層、交易、SQLite、FTS、信任邊界 |
| docs/api.md | 端點、欄位、權限、錯誤、分頁 |
| docs/decisions/ | driver、Evidence、確認流程、tokenizer 等 ADR |
| docs/implementation-plan.md | 範圍、順序、依賴、驗收與待決策 |
| CHANGELOG.md | 繁體中文版本變更 |

AGENTS.md 應包含：KISS／YAGNI、微創異動、使用端小介面、Context 傳遞、參數化查詢、修改前追蹤流程與宣告驗收、修改後格式／vet／測試／diff 檢查、Bug regression tests、禁止未確認大規模重構、API／Schema 異動同步文件。

## 10. 待使用者決策

以下均未在前輪獲明確答覆。實作受影響的 Milestone 前必須確認。

| # | 問題 | 建議 |
| --- | --- | --- |
| 1 | 是否只有一位擁有者、所有 Agent 都受信任？ | MVP 採單一擁有者 |
| 2 | Token 是否必須限制可存取 project？ | 同信任邊界可先範圍約束；跨團隊需 ACL |
| 3 | 誰能 confirmed？ | maintainer 確認，Agent 提交 draft |
| 4 | 是否保留每次修改與確認歷史？ | 稽核需要時第一版納入 revisions |
| 5 | 中文兩字詞與英文子字串是否必須支援？ | 提供實際查詢案例再選 tokenizer |
| 6 | 是否接受 personal／project？personal 代表誰？ | 唯一擁有者的個人資料，不自動混合 |
| 7 | type 固定或自由？ | 小型固定詞彙，如 convention／decision／pitfall／review_finding |
| 8 | confirmed 是否必須有證據？人工決策是否可作來源？ | 至少一項來源，人工決策需紀錄與確認者 |
| 9 | DELETE 只封存，MVP 無恢復／永久刪除是否可接受？ | 先封存，清除政策另定 |
| 10 | 預期筆數、內容大小、同時 Agent 數量？ | 依負載建立資源與延遲驗收 |
| 11 | 第一版是否多機連線？採 HTTPS 或加密隧道？ | 遠端不可明文傳 Token |
| 12 | 新增重試是否必須不重複？ | 自動重試需求存在時做持久化冪等 |
| 13 | Processor 是否立刻需要可靠增量同步？ | 尚未開發則暫緩 |
| 14 | 開源授權是否沿用現有 LICENSE？ | 先確認現有授權，不擅自替換 |
| 15 | 是否接受第 9 節的 Milestone 邊界？ | M1 骨架；M2 持久層；M3 Service；M4 API |
| 16 | module path、監聽埠、設定優先順序與 Token 載入方式？ | M1 開始前定案 |

## 參考資料

以下為前輪規劃採用的官方資料入口；版本與功能於實作時重新查核。

- [Go 發行與支援政策](https://go.dev/doc/devel/release)
- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)
- [mattn/go-sqlite3](https://github.com/mattn/go-sqlite3)
- [SQLite WAL 與 WAL-reset 修正](https://www.sqlite.org/wal.html)
- [SQLite PRAGMA](https://www.sqlite.org/pragma.html)
- [SQLite FTS5](https://www.sqlite.org/fts5.html)
- [SQLite Backup](https://www.sqlite.org/backup.html)
- [Go HTTP Shutdown](https://pkg.go.dev/net/http#Server.Shutdown)
- [Go Race Detector](https://go.dev/doc/articles/race_detector)
