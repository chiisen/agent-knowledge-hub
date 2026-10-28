# 架構與 Milestone 邊界

狀態：Milestone 1 已提交為 44526f9；Milestone 2 本機實作與驗證完成，等待人工 Review。

## Milestone 2

- 依使用者最新授權，本階段同時串接 HTTP Handler → Knowledge Service → Repository → SQLite，不受舊草案將 Service／API 分至 M3／M4 的劃分限制。
- internal/knowledge：資料型別、Repository 小介面、UUID／輸入驗證、confirmed 變更降 draft、版本與封存規則。
- internal/sqlite/repository.go：參數化 CRUD、精確篩選、LIMIT／OFFSET、條件式 UPDATE／RETURNING；不回傳 SQLite 型別給 Service。
- internal/httpapi/knowledge.go：JSON／Content-Type／大小限制、ETag、錯誤映射與安全日誌；保留原 health-only NewHandler，新增 NewKnowledgeHandler 供入口組裝。
- 002_knowledge_crud.sql 新增 version、archived_at 與未封存資料索引；不改寫 001。舊版資料預設 version=1、archived_at=NULL。
- 單筆 SQL 寫入原子提交，UPDATE／封存附版本條件，避免 Service 讀取後遭並行更新覆蓋；不在讀取至寫入間持有長交易。
- 確認權限依使用者決策採可信本機呼叫端，不加入 Token／ACL。confirmed 不代表已驗證來源。
- DELETE 封存以保存來源並區分 deprecated；一般 GET／PATCH／列表看不到，重複 DELETE 可用格式有效的舊 ETag 回 204。
- 預設列表只回 confirmed；每頁最多 100 筆。篩選是資料分類，不是專案授權隔離。
- Context 由 HTTP（5 秒 deadline）傳遞至 Service 和 SQL；所有 I/O 錯誤保留原因。5xx 日誌含 Request ID、method、error_kind 與 SQLite code，不記錄可能含敏感 trigger 文字的 driver 原始錯誤。
- 不建立正式 FTS 索引、搜尋 API、Embedding 或 MCP；既有連線與 HTTP 生命週期設定沿用 M1。

M2 驗證：go build ./...、go test -count=1 ./...、go vet ./...、go test -race -count=1 ./... 均通過（macOS arm64）。API 整合測試使用實體 SQLite，也有實際 loopback HTTP 測試；另以正式入口執行檔驗證 CRUD、412、封存及 SIGTERM exit 0。既有 M1 測試保留，只將已套用 Migration 筆數由 1 同步為 2；001 原文未變。Windows／Linux 未實機驗證。

以下為 M1 基礎設計紀錄；其中「沒有知識 API」描述的是 M1 當時的範圍。當前 API 以 [api.md](api.md) 為準。

## 範圍

依 2026-10-28 使用者最新指示，Milestone 1 納入 HTTP、healthz、SQLite、FTS5 能力檢查與 Migration；覆蓋先前計畫僅包含骨架的階段劃分。不實作 CRUD、搜尋 API、MCP、Embedding、Web UI。

## 組件

- cmd/knowledge-hub：解析啟動參數、組裝依賴、監聽 OS signal，控制 DB／HTTP 生命週期。
- internal/config：flags 解析與安全驗證。module path 依目前 Git remote 採 github.com/chiisen/agent-knowledge-hub。
- internal/httpapi：JSON healthz、request ID、錯誤與必要請求日誌；HTTP 啟停與排空。
- internal/sqlite：檔案路徑編碼、連線設定、FTS5 能力檢查與 Migration runner。

healthz 直接依賴 database/sql 的 PingContext，沒有 Knowledge Service 行為。未來知識 API 依 Handler → Knowledge Service → Repository → SQLite 建立；本階段不製造尚無用途的 Service／Repository 空介面。

## 安全與資源

僅接受 literal loopback IP，預設 127.0.0.1:8080；不提供遠端開關或 Token，以免未具備知識 API 授權前開放網路。Request ID 由伺服器產生，不信任外部標頭。

僅一個 SQLite 連線，無背景排程。以固定 driver PRAGMA 選項配置每個連線：foreign_keys=1、busy_timeout=2000、synchronous=FULL、cache_size=-2048、wal_autocheckpoint=1000。啟動時設定並讀回 WAL；不增加多個讀寫池。

FTS5 probe 使用同一實體連線上的 temp 虛擬表，完成後移除，不建立知識索引或決定 tokenizer。SQLite 驅動固定 v1.61.0，本次本機測試內嵌版本為 3.54.1。

所有外部資料使用 SQL 參數；固定 DDL、PRAGMA 及受版本控制的 Migration 不接受請求輸入。路徑透過 URL 編碼，避免 ?／# 成為 DSN 選項。

## Migration

runner 要求 001 起連續編號的 SQL 檔名，記錄 version、name、checksum、applied_at。Migration 與歷史在同一交易；失敗整批回滾。再次執行驗證歷史而不重做已套用 SQL；未知版本、斷號或 checksum 不符即停止。

001_initial.sql 透過 embed.FS 包入執行檔，建立已確認的最小 knowledge 表：

| 欄位 | 型別／約束 |
| --- | --- |
| id | TEXT PRIMARY KEY NOT NULL，用於 UUID 字串 |
| title、content、type、scope | TEXT NOT NULL，trim 後長度大於 0 |
| project、module | TEXT，可為 NULL |
| status | TEXT NOT NULL DEFAULT draft，限 draft／confirmed／deprecated |
| source | TEXT NOT NULL，不強制 JSON |
| created_at、updated_at | INTEGER NOT NULL，應用約定 UTC Unix milliseconds |

SQL 不檢查 UUID 格式，不自動產生 ID／時間；目前沒有資料寫入 API。type／scope 不限制詞彙；version、archived_at、確認資訊、Evidence 與 revisions 留後續評估及新 Migration，不提前建立。

只支援單一程序啟動管理資料庫，不承諾多程序同時 Migration。

## HTTP 與關機

GET /healthz 以 1 秒 Context deadline Ping DB；健康回 200，失敗回 503。錯誤 body 只回穩定 code、message、request_id，內部原因寫入 slog。未匹配路徑與不支援方法回 JSON 404／405。

HTTP timeout：header 5 秒、read 10 秒、write 10 秒、idle 60 秒。收到取消後停止 listener，給進行中請求 10 秒完成；超時取消 request context 並強制關閉連線。排空後關閉 DB。正常結束依 SQLite 關閉連線行為處理 WAL，不新增常駐 checkpoint worker。

日誌寫 stderr，僅記錄 method、route template、status、duration、request ID 與必要內部錯誤。未匹配路徑使用 unmatched，避免任意路徑內容進入日誌。

## 測試與限制

測試涵蓋設定拒絕、healthz、錯誤遮蔽、敏感值日誌、HTTP 排空／超時／listener 錯誤、PRAGMA 重連、FTS5、Migration 冪等／回滾／歷史異動。

macOS arm64 已通過 go build ./...、go test ./...、go vet ./...、go test -race ./...。另以實際執行檔驗證兩次 healthz 200、SIGTERM exit 0、Migration 冪等與資料保留。Windows／Linux 尚未實機驗證；尚無效能 SLA，完整知識 API 仍未實作。
