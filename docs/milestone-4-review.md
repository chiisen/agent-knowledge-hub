# Issue #2／Milestone 4 Review Report

日期：2026-10-28

## 實作摘要

- SQLite 連線池上限調為 4，保持 driver URI 對每條實體連線套用 foreign_keys、busy_timeout=2000ms、synchronous=FULL 等 PRAGMA。SQLite BUSY／LOCKED 仍以安全分類回 503 `database_busy`。
- Handler 以有界 response buffer 限制 JSON 回應為 1 MiB，超限回 500 `response_too_large`；新增 panic recovery 回 500 `internal_error`。兩者保留 Request ID，panic 日誌不含 panic 值。
- 原有 structured JSON slog、伺服器產生的 Request ID、5 秒 API deadline、HTTP timeout 與 Graceful Shutdown 保留；request deadline 後回 503。
- HTTP 排空後，入口以 5 秒 context 執行 SQLite WAL `TRUNCATE` checkpoint，再關閉 DB。
- 新增 24 Client HTTP 併發新增／更新／讀取測試、DB lock/timeout、SQLite 多連線 PRAGMA、`integrity_check`、checkpoint、離線備份還原測試與 benchmark。
- README、架構、實作計畫、CHANGELOG 與任務狀態同步更新。停機備份與還原步驟見 README。

## 測試環境與實測數據

- OS／架構：macOS Darwin arm64；CPU：Apple M6。
- Go：1.27.3；SQLite：Go module `modernc.org/sqlite v1.61.0` 內嵌版本（目前測試記錄為 3.54.1）。
- HTTP 併發案例：24 clients、24 筆知識資料；每一階段同時發出 24 個請求。數據為單次 loopback 測量，延遲是每個請求 wall-clock，throughput 是階段請求數除以總階段時間。

| API 操作 | Throughput | p50 | p95 |
| --- | ---: | ---: | ---: |
| Create | 1,082 req/s | 4.58 ms | 11.38 ms |
| Update | 2,326 req/s | 4.02 ms | 6.98 ms |
| Read | 13,312 req/s | 1.43 ms | 1.59 ms |

固定 1,000 次 Go benchmarks（`-benchmem -benchtime=1000x`）：

| Benchmark | ns/op | B/op | allocs/op | Throughput 換算 |
| --- | ---: | ---: | ---: | ---: |
| Health handler | 6,568 | 8,140 | 48 | 約 152,253 ops/s |
| Knowledge list（預置 100 筆） | 50,782 | 51,165 | 527 | 約 19,692 ops/s |
| SQLite create | 138,203 | 2,713 | 74 | 約 7,235 ops/s |

SQLite create benchmark 完成 1,000 筆新增並 checkpoint 後，DB 為 495,616 bytes，WAL 為 0 bytes。這是含 schema、FTS 索引和該筆資料集的單次檔案大小，不代表每筆固定成長量。

同一次 macOS `/usr/bin/time -l` 包住 benchmark 命令：real 0.89 秒、user 0.59 秒、system 0.46 秒、最大 RSS 220,856,320 bytes（約 210.6 MiB）。此 RSS 是 Go benchmark 測試程序／runner 的峰值，並非獨立服務常駐記憶體；ns/op 與 B/op 提供每次操作的 CPU 時間代理量和配置量。

數字為本機短測樣本，不構成 SLA。Apple M6、系統負載、Go/SQLite 版本、資料內容、快取狀態與 loopback 都會影響結果。

## 驗收清單

- [x] 多個 HTTP Client 同時讀取。
- [x] 多個 HTTP Client 同時新增與更新。
- [x] SQLite Busy Timeout 2 秒；持有 exclusive lock 時 API 回 503 `database_busy`。
- [x] API deadline 5 秒、HTTP timeout 設定；deadline 測試回 503 `request_timeout`。
- [x] Graceful Shutdown 排空測試通過；服務排空後執行 WAL TRUNCATE checkpoint。
- [x] JSON structured logging、request ID 與敏感錯誤遮蔽測試保留並通過。
- [x] Panic recovery 測試確認 500 JSON 且不洩漏 panic detail。
- [x] SQLite checkpoint 與 DB/WAL 大小實測完成。
- [x] API response 上限 1 MiB，超限回穩定 JSON 錯誤。
- [x] Health／List／SQLite create benchmark 記錄耗時、配置與 allocation；記錄 benchmark runner CPU/RSS。
- [x] SQLite `PRAGMA integrity_check` 回 `ok`。
- [x] 停機 checkpoint 後備份檔可重新開啟、Migration 驗證並讀出資料。
- [x] 未新增 Redis、PostgreSQL、Docker、大型 Framework、MCP 或 Vector Search。

## 驗證命令與結果

- `gofmt -w ...`：完成；`gofmt -l .` 無輸出。
- `git diff --check`：通過。
- `go build ./...`：通過。
- `go test -count=1 ./...`：通過（cmd、config、httpapi、knowledge、sqlite）。
- `go test -race -count=1 ./...`：通過，無 race report。
- `go vet ./...`：通過。
- `go test -run '^$' -bench 'Benchmark(HealthHandler|KnowledgeList|SQLiteCreate)$' -benchmem -benchtime=1000x -count=1 ./internal/httpapi ./internal/sqlite`：通過；結果如上。
- Windows／Linux 實機驗證：未執行。

## 剩餘風險與下一階段前置條件

- SQLite WAL 仍是單寫者；四連線只允許讀取與工作排程並行，不會讓寫入平行提交。高寫入競爭可能仍回 `database_busy`。
- response buffer 為每個正在處理的回應暫存最多 1 MiB；大量同時產生大型回應時，暫時記憶體仍會隨活躍請求數增加。尚未設全域 HTTP concurrency 上限。
- 備份流程是離線單檔複製；沒有線上 snapshot、排程或備份輪替政策。部署者需自行管理異地副本與保留期。
- 24 clients／24 records 與 1,000 次 microbenchmark 不代表長時間、較大資料量或多機網路負載；尚未設定容量 SLA。
- 遠端認證、ACL、線上備份、Windows/Linux 實機矩陣及更大資料集 benchmark 均留後續決策；Issue #2 不要求本次實作。
