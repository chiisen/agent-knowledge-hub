# agent-knowledge-hub 開發規範

## 範圍與工作流程

- 使用繁體中文溝通、撰寫開發文件與 Commit 訊息。
- 僅執行使用者指定 Milestone；完成後交付 Review Report 並停止。
- 不得自行 Commit、Push、發佈或進入下一階段。
- 開始前閱讀 README.md、本文件、docs/architecture.md、docs/implementation-plan.md；若有 GEMINI.md，優先遵循其專案規則。
- 檢查 Git 狀態，保留所有既有未提交修改，不覆蓋不屬於本次工作範圍的檔案。
- 修改前宣告驗收目標與範圍；需求或重大架構決策不明確時先詢問。
- 遵守 KISS、YAGNI；禁止未確認的大規模重構與無關格式調整。

## Go 慣例

- 使用受支援的 Go 穩定版本及 gofmt；標準庫優先，不引入大型 Framework。
- 以具體型別開始，只在實際消費端需要時定義小介面。
- HTTP 與資料庫分離；後續 Knowledge 邏輯依 Handler → Service → Repository → SQLite 實作，不建立空的預期抽象。
- context.Context 為 I/O 函式第一參數，傳遞取消與期限，不存放於長生命週期 struct。
- 錯誤加上操作上下文並用 %w 保留原因；僅在邊界記錄必要日誌。
- 使用 slog；禁止記錄 Token、Authorization、知識全文、來源摘錄、搜尋字串或完整 query string。
- 使用 net/http 的 timeout 與 Graceful Shutdown；關機先排空請求，再關閉資料庫。

## SQLite

- 不使用 Docker 或外部常駐資料庫進行開發／測試。
- 所有外部資料使用參數化 SQL；不得把使用者輸入拼成 SQL、PRAGMA、DSN 或排序欄位。
- 固定 Migration 與 PRAGMA 是受版本控制的可信 SQL，不接受 API 提供任意 SQL。
- 每個實體連線套用 Foreign Keys、Busy Timeout 等設定；啟動確認 WAL 與 FTS5。
- 已發佈 Migration 不可改寫，使用新版本 Migration；保留 checksum 驗證。
- DB、WAL、SHM、journal 不加入 Git；使用本機磁碟，避免同步資料夾與網路檔案系統。

## 測試與交付

- 行為修改先建立可失敗的測試，再實作最小功能；Bug 必須附 Regression Test。
- 不得為通過測試刪除、跳過或弱化測試。
- SQLite integration tests 使用 t.TempDir() 實體檔案，覆蓋初始化、Migration 冪等與回滾。
- HTTP lifecycle tests 需要本機 loopback 臨時埠；沙箱拒絕時請求升權重跑，不跳過。
- 完成後執行 gofmt、go build ./...、go test ./...、go vet ./...、go test -race ./...、git diff --check。
- 額外檢查新增未追蹤檔案的空白格式；不要為了檢查而擅自 stage。
- 依異動更新 README、architecture、implementation-plan、CHANGELOG 與既有任務狀態。
- 回報功能、檔案、測試命令與實際結果、未解問題、Review 風險、下一階段前置條件。
- 未執行的測試或平台驗證明示未驗證；編譯成功不等於實機驗收。
