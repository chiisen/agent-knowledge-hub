# agent-knowledge-hub

輕量、可自行部署的 AI Agent 長期知識 API Server。使用 Go、net/http 與 SQLite，不使用 Docker 或大型 Framework。

Milestone 1 已提交為 44526f9。Milestone 2 完成 Knowledge CRUD、ETag 版本條件與封存；Milestone 3 新增 SQLite FTS5 trigram 全文搜尋。MCP、Embedding 與 Web UI 尚未實作。完整契約見 [docs/api.md](docs/api.md)。

## 開發環境

- Go 1.27.3 或相容的受支援更新版。
- SQLite 使用 modernc.org/sqlite v1.61.0，正式程式不需要 CGO 或外部 sqlite3 指令。
- 本次本機 SQLite 初始化測試觀察到的內嵌版本為 3.54.1。
- 首次建置需下載 Go modules；下載的編譯工具依賴不表示程式會啟動外部服務。
- Race Detector 可能需要 C 編譯器；這與正式程式的無 CGO 建置分開。

## 啟動方式

啟動前自動初始化 SQLite 並執行嵌入執行檔的 Migration；失敗時不開放 HTTP listener：

```sh
go run ./cmd/knowledge-hub -addr 127.0.0.1:8080 -db data/knowledge.db
curl http://127.0.0.1:8080/healthz
```

- 僅支援 literal loopback IP；拒絕 0.0.0.0、::、空 host 與網域名稱。
- 可用 -addr 127.0.0.1:0 配置作業系統分配的臨時埠；實際位址由 JSON 日誌輸出。
- 只使用 flags，沒有隱含環境變數覆寫；資料庫參數是本機檔案路徑，不接受任意 DSN。
- 成功健康回應為 `{"status":"ok"}`；DB 不可用時回 503 與不洩漏內部資訊的 JSON。
- 日誌寫至 stderr；不記錄 Authorization、body、查詢字串或知識內容。
- Ctrl+C／SIGTERM 觸發 Graceful Shutdown；先排空 HTTP，最多等候 10 秒，再取消請求並關閉 DB。
- 本階段提供無 Token 的本機 healthz 與 Knowledge CRUD；同機呼叫端均視為可信，可確認與修改知識。Token 與遠端服務尚未實作，不可將服務轉送公開。

## SQLite

- 單一連線，啟用 foreign_keys、WAL、busy_timeout=2000ms、synchronous=FULL。
- 每個新實體連線套用連線級 PRAGMA；啟動時使用暫存虛擬表確認 FTS5，隨即移除。
- FTS5 trigram 索引 title／content，支援至少 3 Unicode 字元的子字串查詢；索引以 SQLite trigger 同步，FTS 表使用 external content 避免複製知識全文。
- Migration runner 以交易維護 schema_migrations 與版本／檔名／SHA-256 checksum；拒絕已套用內容變動與未知版本。
- knowledge 採已確認的最小 Schema：id 為 UUID 字串用途的 TEXT 主鍵；title、content、type、scope 非空，project／module 可為 NULL，status 預設 draft 並限 draft／confirmed／deprecated，source 為純 TEXT，時間為 UTC Unix milliseconds。
- type／scope 暫不限詞彙，source 不強制 JSON；不建立 Evidence 表、修訂歷史或確認欄位。CRUD 層驗證 UUID、必要欄位、字串大小與狀態，時間由伺服器產生。
- 002_knowledge_crud.sql 新增 version 與 archived_at；保留 001_initial.sql，既有資料升級後 version=1、未封存。PATCH／DELETE 必須帶 If-Match，封存後一般 API 隱藏資料；列表預設 confirmed、每頁最多 100 筆。
- 已發佈 Migration 不可改寫，以新版本變更 Schema。
- DB 放本機磁碟；不使用網路磁碟或同步中的雲端資料夾。資料未加密，目錄權限由部署者管理。
- .gitignore 排除預設 data/、一般 SQLite 副檔名及 WAL／SHM／journal；自訂無副檔名的外部路徑需自行管理，不可強制加入 Git。
- 正常停機後再備份；運作中不能只複製 .db 就視為完整備份。

## 驗證

```sh
go build ./...
go test ./...
go vet ./...
go test -race ./...
gofmt -l .
git diff --check
```

SQLite integration tests 使用暫存本機檔案；HTTP 啟停測試使用 loopback 臨時埠。沙箱若禁止 bind，應經核准重跑，不跳過測試。

本次 macOS arm64 驗證：build／test／vet／race 通過；實際執行檔兩次啟動 healthz 回 200，SIGTERM 以 0 結束，Migration 只記錄一次且重啟保留資料。Windows／Linux 尚未實機驗證，效能基準留後續 Milestone。

Milestone 2 另通過五個套件的全套測試與 Race Detector，包含 CRUD、輸入、分頁／篩選、ETag 競爭、封存、取消與錯誤保密測試。Milestone 3 新增搜尋 API，並將 Migration 升為三筆；FTS 搜尋契約與限制見 API 文件。

架構見 [docs/architecture.md](docs/architecture.md)，階段與未決事項見 [docs/implementation-plan.md](docs/implementation-plan.md)，開發規則見 [AGENTS.md](AGENTS.md)。
