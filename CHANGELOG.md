# 更新紀錄

本文件依循 [Keep a Changelog](https://keepachangelog.com/zh-TW/1.1.0/) 格式。

## [Unreleased]

### 修正

- Windows SQLite 初始化測試改用合法的 `folder #` 路徑；其他平台保留 `folder ?#`。保留特殊字元路徑、初始化與連線 PRAGMA 驗證，不跳過測試或修改產品行為。

### 新增

- 記錄 macOS 使用既有 `scripts/memory-smoke.ps1` 的跑法與 2026-11-10 實機結果。未另寫腳本，也未修改產品行為。Linux 仍未實機驗證。

- 新增 Issue #7 Review Report：macOS arm64 以編譯執行檔驗收 Project 白名單。未修改產品行為。Windows／Linux 未實機驗證。

- 新增 PowerShell 7 一鍵記憶驗收腳本：建置實際執行檔，使用暫存 SQLite 與臨時 loopback 埠，驗證確認、中文搜尋、ETag 衝突、強制重啟持久化與封存；輸出 PASS／FAIL，不顯示 Token，結束時清理測試程序與資料。

- 新增 Windows 記憶功能 Review Report 與獨立測試資料庫的 PowerShell 操作流程；記錄持久化、搜尋索引、備份還原通過，以及初始化測試路徑不相容 Windows 的失敗結果。未修改產品行為。

- Issue #8：知識進入 confirmed 時記錄 Token 名稱與確認時間。deprecated 保留，回到 draft 清空。單筆與列表回傳這兩欄，搜尋不回。

- Issue #5：API Token 綁定 project 白名單。列表與搜尋只回允許的 project；單筆越權回 404，新增或把 project 改到白名單外回 403。空 project 沒有 Token 可以讀寫。

- Issue #4：本機 CLI 核發、列出、撤銷與輪替 API Token。伺服器只存 SHA-256；Knowledge CRUD 與搜尋要求 Bearer Token，`/healthz` 免認證。未設定到期的 Token 持續有效直到撤銷。

- Issue #2／Milestone 4：SQLite 有界四連線池、連線 PRAGMA 驗證、Busy/Locked 回應與 WAL checkpoint；HTTP panic recovery、1 MiB 回應上限及併發／容量 benchmarks。
- 新增停機備份／還原流程、SQLite integrity／備份還原測試，以及 24 Client HTTP 併發讀寫測量；實測數據與限制見 `docs/milestone-4-review.md`。

- Milestone 3：POST /api/v1/knowledge/search，SQLite FTS5 trigram title／content 子字串搜尋、BM25 排序、confirmed 預設與可選 status、專案篩選、有界分頁及摘要片段。
- 新增 003 FTS migration、既有資料回填與 INSERT／內容更新索引 trigger；封存結果從搜尋排除。中文搜尋需至少三字，特殊 FTS 語法以安全 phrase 查詢處理。
- 搜尋 Service、SQLite migration／索引同步及 HTTP 驗證測試。

- Milestone 2：分層 Knowledge CRUD REST API、UUID 與輸入驗證、精確篩選／有界分頁、ETag／If-Match 條件更新、confirmed 變更降 draft 與 archived_at 封存。
- 新增 002 Migration、M1 資料升級驗證、並行更新／取消／API 整合測試與 docs/api.md；保留既有 M1 測試，僅同步預期 Migration 筆數。
- 知識 API 錯誤以 Request ID 追查，日誌只記錄安全分類與 SQLite code，避免洩漏 driver／trigger 敏感原文。
- M2 的 macOS arm64 build／test／vet／race、loopback API 整合與實際執行檔 CRUD／關機驗證通過。

- 專案需求、架構設計、SQLite 與 REST API 草案、測試策略及分階段實作計畫；明列待確認決策與 Milestone Review 邊界。
- Milestone 1：Go Module、loopback 設定、JSON healthz、slog、HTTP 排空及 SQLite 初始化／FTS5 能力檢查與交易式 Migration runner。
- 使用者確認的最小 knowledge Schema、embedded 初版 Migration，以及冪等、重啟資料保留、約束、回滾與歷史 checksum 測試；macOS arm64 的 build／test／vet／race 與實際啟停驗證通過，等待人工 Review。
- 專案 AGENTS 規範、架構與本機開發文件，以及 SQLite／執行檔 Git 排除規則。
- 排除 macOS metadata、本機環境檔與日誌，保留環境設定範本可納入版控。
