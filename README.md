# agent-knowledge-hub

輕量、可自行部署的 AI Agent 長期知識 API Server。使用 Go、net/http 與 SQLite，不使用 Docker 或大型 Framework。

目前已完成 Milestone 1 的本機實作與驗證，等待人工 Code Review：專案骨架、HTTP Server、SQLite 初始化與版本化 Migration。不提供 Knowledge CRUD、搜尋 API、MCP、Embedding 或 Web UI。

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
- 本階段僅提供無 Token 的本機 healthz；Token 與遠端服務不在此階段實作。

## SQLite

- 單一連線，啟用 foreign_keys、WAL、busy_timeout=2000ms、synchronous=FULL。
- 每個新實體連線套用連線級 PRAGMA；啟動時使用暫存虛擬表確認 FTS5，隨即移除。
- 不建立正式知識全文索引，避免提前決定 tokenizer。
- Migration runner 以交易維護 schema_migrations 與版本／檔名／SHA-256 checksum；拒絕已套用內容變動與未知版本。
- knowledge 採已確認的最小 Schema：id 為 UUID 字串用途的 TEXT 主鍵；title、content、type、scope 非空，project／module 可為 NULL，status 預設 draft 並限 draft／confirmed／deprecated，source 為純 TEXT，時間為 UTC Unix milliseconds。
- type／scope 暫不限詞彙，source 不強制 JSON；不建立 Evidence 表、修訂歷史或確認欄位。UUID 格式與寫入時間驗證留待後續 CRUD 層，不宣稱目前 SQL 有完整輸入驗證。
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

架構見 [docs/architecture.md](docs/architecture.md)，階段與未決事項見 [docs/implementation-plan.md](docs/implementation-plan.md)，開發規則見 [AGENTS.md](AGENTS.md)。
