# Issue #7 Review Report

日期：2026-11-10。環境：macOS 27.2、darwin/arm64、Go 1.27.3。
範圍：編譯 `cmd/knowledge-hub`，對暫存 SQLite 與作業系統分配的 `127.0.0.1` 埠做 Project 白名單手動驗收。未修改產品程式，未綁非 loopback 位址。

Windows 與 Linux 本次沒有實機執行，標成未驗證。

## 結果

全部檢查通過。明文 Token 不寫入本報告。

| 檢查 | 實際結果 |
| --- | --- |
| `token issue` 未帶 `-project` | 退出碼 1。錯誤為 `at least one project is required`。輸出沒有 `token` 欄位 |
| `token issue -project alpha -project beta` | 白名單為 `alpha`、`beta`。stdout JSON 的明文只出現一次 |
| 白名單內新增、讀取、列表、搜尋、封存 | POST 201、GET 200、`status=draft` 列表 200 且含該筆、搜尋 `query=alpha` 200 且 1 筆、DELETE 204、再 GET 404 |
| 另一把只允許 `other` 的 Token 用 UUID 讀取、更新、封存 | GET／PATCH／DELETE 都是 404。回應不含標題。原 Token 再讀仍 200 |
| 新增到白名單外、省略 project、把 project 改到白名單外 | 三個請求都是 403。被拒的 PATCH 之後，project 仍是 `alpha`、version 仍是 1 |
| 列表與搜尋不回白名單外資料 | 對方列表與搜尋皆 200、0 筆，不含該標題。指定 `project=alpha` 篩選也是 0 筆 |
| `scope=personal` 且未帶 project | POST 403 |
| `token rotate` 未帶 `-project` | 新 Token 白名單仍是 `alpha`。舊 Token 讀取 401，新 Token 讀取 200 |
| `token rotate -project beta` | 白名單整組換成 `beta`。被替換的 Token 401。新 Token 讀 alpha 為 404 且不含標題，新增 beta 為 201 |
| 沒有 project 列的舊 Token | 直接寫入 `api_tokens`、不寫 `api_token_projects`。列表 200 且 0 筆，單筆 GET 404，POST 403。不是 401 |
| 伺服器日誌 | 不含本次 Token 明文、`Authorization` 或 `Bearer` |
| SIGTERM 後重啟 | 程序退出碼 0。同一 Token 再讀 project 仍是 `alpha`。`token list` 的白名單與撤銷狀態仍在 |

舊 Token 無法用現在的 `token issue` 產生，所以這一步用本機 SQLite 插入一列雜湊，模擬 Issue #5 之前的資料。服務仍由編譯出的執行檔處理 HTTP。

列表與搜尋在沒有可見資料時回 200 空陣列，與 API 契約一致；單筆越權是 404，寫出白名單是 403。

## 未驗證

- Windows、Linux 實機。
- 斷電或複製運作中資料庫的耐久性。這次只驗證 SIGTERM 後正常重啟。
