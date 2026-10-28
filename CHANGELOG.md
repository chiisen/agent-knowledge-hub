# 更新紀錄

本文件依循 [Keep a Changelog](https://keepachangelog.com/zh-TW/1.1.0/) 格式。

## [Unreleased]

### 新增

- Milestone 2：分層 Knowledge CRUD REST API、UUID 與輸入驗證、精確篩選／有界分頁、ETag／If-Match 條件更新、confirmed 變更降 draft 與 archived_at 封存。
- 新增 002 Migration、M1 資料升級驗證、並行更新／取消／API 整合測試與 docs/api.md；保留既有 M1 測試，僅同步預期 Migration 筆數。
- 知識 API 錯誤以 Request ID 追查，日誌只記錄安全分類與 SQLite code，避免洩漏 driver／trigger 敏感原文。
- M2 的 macOS arm64 build／test／vet／race、loopback API 整合與實際執行檔 CRUD／關機驗證通過。

- 專案需求、架構設計、SQLite 與 REST API 草案、測試策略及分階段實作計畫；明列待確認決策與 Milestone Review 邊界。
- Milestone 1：Go Module、loopback 設定、JSON healthz、slog、HTTP 排空及 SQLite 初始化／FTS5 能力檢查與交易式 Migration runner。
- 使用者確認的最小 knowledge Schema、embedded 初版 Migration，以及冪等、重啟資料保留、約束、回滾與歷史 checksum 測試；macOS arm64 的 build／test／vet／race 與實際啟停驗證通過，等待人工 Review。
- 專案 AGENTS 規範、架構與本機開發文件，以及 SQLite／執行檔 Git 排除規則。
- 排除 macOS metadata、本機環境檔與日誌，保留環境設定範本可納入版控。
