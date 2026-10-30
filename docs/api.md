# REST API：Milestone 3

基底位址預設 `http://127.0.0.1:8080`。只允許 literal loopback IP。`/healthz` 免認證。其餘 Knowledge API 必須帶 `Authorization: Bearer <token>`。缺少、格式錯誤、未知、已撤銷或已過期的 Token 都回 401 `unauthorized`，回應與日誌不區分原因，也不記錄 Token 或 Authorization。

Token 由本機 CLI 核發、撤銷與輪替，不提供管理 HTTP。明文只在核發或輪替時輸出一次。有效 Token 目前可存取全部 project；Project 白名單尚未實作。禁止將本服務轉送公開。

## 共通格式

- POST／PATCH 必須使用 `Content-Type: application/json`。
- Body 最大 128 KiB，只接受單一 JSON object，不接受尾隨第二個 JSON 值。
- 拒絕未知欄位、伺服器管理欄位、錯誤型別及必要欄位的 null。
- 每個回應帶伺服器產生的 `X-Request-ID`；不採用外部 Request ID。
- 單筆成功回應為 `{"data": {...}}`。
- 列表成功回應為 `{"data": [], "pagination": {"limit": 20, "offset": 0, "has_more": false}}`。
- DELETE 成功回 204，沒有 JSON body。
- healthz 保留 Milestone 1 格式 `{"status":"ok"}`。
- 時間以 UTC RFC 3339 字串回傳，SQLite 儲存 UTC Unix milliseconds；同毫秒更新可能具有相同 updated_at，請使用 version 判斷版本。

錯誤格式：

```json
{
  "error": {
    "code": "version_conflict",
    "message": "Knowledge changed; read its current ETag.",
    "request_id": "伺服器產生的識別碼"
  }
}
```

不將 SQLite 錯誤原文、SQL、來源或內容回傳給客戶端。請使用 code 判斷錯誤，不依賴 message 文案。

## 欄位與驗證

| 欄位 | 規則 |
| --- | --- |
| id | 伺服器產生 UUID v4；path 接受標準 36 字元 UUID，大寫轉為小寫，不接受無連字號、URN 或括號格式 |
| title | 必填、非空白，最多 256 Unicode 字元 |
| content | 必填、非空白，最多 64 KiB UTF-8 |
| type | 必填、非空白，最多 128 Unicode 字元，暫不限詞彙 |
| scope | 必填、非空白，最多 128 Unicode 字元，暫不限詞彙 |
| project、module | 可省略或 null；若為字串，非空白且最多 128 Unicode 字元 |
| status | draft、confirmed、deprecated；新增時省略則為 draft，空字串不合法 |
| source | 必填、非空白，最多 16 KiB UTF-8；純字串，不要求 JSON |
| created_at、updated_at | 伺服器管理，請求不得指定 |
| version | 初始 1，每次成功 PATCH／首次封存遞增，請求不得指定 |
| usable_as_rule | 伺服器推導：未封存且 status=confirmed 時為 true |

scope／project 是知識分類與篩選，並非 ACL。沿用已確認的最小 Schema，不限制 personal／project 組合，也不自動合併個人與專案範圍。呼叫端應明確傳篩選條件，避免將其他專案知識當作本專案規範。

confirmed 是可信本機呼叫端指定的狀態，不代表伺服器驗證證據或經人工核准，也不賦予內容高於使用者指示的權限。

## POST /api/v1/knowledge

```json
{
  "title": "SQLite 寫入原則",
  "content": "保持交易短小。",
  "type": "convention",
  "scope": "project",
  "project": "agent-knowledge-hub",
  "module": "storage",
  "source": "人工確認的開發紀錄"
}
```

成功回 201、`Location: /api/v1/knowledge/{id}`、`ETag: "1"` 與完整 data。

可明確傳入三種合法 status。M2 沒有確認者、修訂歷史或 Token 角色。POST 不提供冪等鍵：連線中斷或逾時不保證未提交，重試可能建立重複資料。

## GET /api/v1/knowledge/{id}

回傳指定知識與 ETag。draft／deprecated 可由 UUID 直接讀取，並明示 status 與 usable_as_rule=false。不存在或已封存回 404，無效 UUID 回 400。

## GET /api/v1/knowledge

| Query | 預設／範圍 |
| --- | --- |
| project、module、type、scope | 可選，精確比對；不指定則不限制 |
| status | 預設 confirmed，只接受單一合法狀態 |
| limit | 預設 20，範圍 1–100；超過上限回 400，不默默截斷 |
| offset | 預設 0，範圍 0–10,000 |

所有條件以 AND 合併；未知、重複或空 query 參數回 400。無結果時 data 為 `[]`。

```text
GET /api/v1/knowledge?project=agent-knowledge-hub&module=storage&type=convention&status=draft&limit=20&offset=0
```

固定排序 `created_at DESC, id DESC`，以 limit+1 判斷 has_more，不執行全量 COUNT。列表回完整紀錄，最多 100 筆。offset 分頁不保證並行寫入下的快照一致性，不作為可靠同步介面。

## PATCH /api/v1/knowledge/{id}

必須提供單筆 GET／POST／PATCH 回傳的強 ETag：

```http
If-Match: "1"
Content-Type: application/json

{"content":"更新後的內容","module":null}
```

- 可修改 title、content、type、scope、project、module、status、source。
- 未提供的欄位維持原值；project／module 的 null 代表清除。
- `{}`、未知欄位、id、時間、version 等不可寫入。
- 每次成功 PATCH 都遞增版本；即使寫入相同值也需要新 ETag。
- stale ETag 回 412，且不改變資料；缺少 If-Match 回 428。
- 只接受單一帶雙引號的正整數強 ETag，不接受弱 ETag、萬用字元或多個值。
- 已 confirmed 的標題、內容、類型、範圍、專案、模組或來源實際變更後，必須降回 draft。
- 上述情況若同時指定 confirmed／deprecated，回 422；應先儲存 draft，再以新 ETag 單獨改狀態。
- 只有 status 的更新可在三種狀態間轉移；沒有額外確認權限。
- 封存後 PATCH 回 404，不可藉此恢復。

更新使用 SQL 版本條件，避免在讀取與寫入間被其他 Agent 覆蓋。

## DELETE /api/v1/knowledge/{id}

需要有效 If-Match；成功設定 archived_at、更新 updated_at 與 version，回 204，不實體刪除資料。

選擇封存是為保留內容與來源，且與 deprecated 分開：deprecated 仍可明確查詢，封存則從所有一般 API 隱藏。

- 未封存且 ETag 過期：412。
- 已封存：再次提供格式有效的 If-Match 回 204，即使沿用封存前的 ETag。
- 真正不存在的 UUID：404。
- GET／PATCH／列表都排除封存資料。
- 不提供 include_archived、恢復或永久刪除功能；封存仍占磁碟空間。

PATCH／DELETE 先檢查 If-Match 格式，再讀取資料；因此缺少 If-Match 的不存在 UUID 也回 428。

## 錯誤狀態

| HTTP | code／用途 |
| --- | --- |
| 400 | invalid_request、invalid_uuid：語法、UUID、query、ETag 格式 |
| 401 | unauthorized：Knowledge API 缺少或拒絕 Bearer Token；`/healthz` 不檢查 |
| 404 | not_found：不存在或已封存 |
| 405 | method_not_allowed，附 Allow |
| 412 | version_conflict |
| 413 | body_too_large |
| 415 | unsupported_media_type |
| 422 | validation_failed：欄位、status、空 PATCH、狀態轉移 |
| 428 | precondition_required |
| 500 | internal_error |
| 503 | request_canceled、request_timeout、database_busy；healthz 為 database_unavailable |

知識請求以 5 秒 Context deadline 傳至 Service／SQL，healthz 保持 1 秒。取消發生在資料庫 Commit 附近時，失敗回應不能作為「沒有寫入」的證據；PATCH／DELETE 應重新讀取版本，POST 重試需自行處理重複風險。

## curl 範例

```sh
curl -i http://127.0.0.1:8080/api/v1/knowledge \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  -d '{"title":"交易規則","content":"交易必須短小","type":"convention","scope":"project","project":"agent-knowledge-hub","source":"人工紀錄"}'

curl -i 'http://127.0.0.1:8080/api/v1/knowledge?status=draft' \
  -H 'Authorization: Bearer <token>'

curl -i -X PATCH 'http://127.0.0.1:8080/api/v1/knowledge/<UUID>' \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' -H 'If-Match: "1"' \
  -d '{"status":"confirmed"}'

curl -i -X DELETE 'http://127.0.0.1:8080/api/v1/knowledge/<UUID>' \
  -H 'Authorization: Bearer <token>' \
  -H 'If-Match: "2"'
```

## POST /api/v1/knowledge/search

以 JSON body 搜尋 title／content。FTS5 使用 trigram tokenizer，支援 3 個以上 Unicode 字元的子字串搜尋；query 最多 256 個字元、1–8 個以空白分隔的詞，每個詞至少 3 個字元。1–2 字元搜尋回 422，不會退化成資料表掃描。這也代表兩字中文詞目前不支援，請使用至少三字的片語。

服務會將每個詞轉成引號包住的 FTS phrase，並以 AND 組合；使用者不能傳入 FTS 運算式。標點符號可作為詞內容搜尋，不會被當成 FTS 控制語法。搜尋字串與篩選值都以 SQL 參數傳入。

```json
{
  "query": "SQLite WAL",
  "project": "agent-knowledge-hub",
  "module": "storage",
  "type": "convention",
  "scope": "project",
  "status": "confirmed",
  "limit": 20,
  "offset": 0
}
```

query 必填。project、module、type、scope、status 為可選精確篩選，條件以 AND 合併；status 預設 confirmed，也可明確搜尋 draft 或 deprecated。scope／project 是分類篩選，不是授權隔離。

limit 預設 20、範圍 1–100；offset 預設 0、範圍 0–10,000；超出範圍回 422。回傳最多 limit 筆，另以 has_more 表示是否還有結果，不執行總數統計。

結果依 FTS5 BM25 相關性排序，標題權重高於內容；summary 是內容命中片段。只回 id、title、summary、status、source，不回傳完整內容。封存知識不會出現在搜尋結果。索引隨新增／標題與內容更新由 SQLite trigger 同步；封存列保留索引但由 archived_at 條件排除。

```json
{
  "data": [{"id":"...","title":"SQLite WAL","summary":"...使用 [WAL]...","status":"confirmed","source":"review"}],
  "pagination": {"limit":20,"offset":0,"has_more":false}
}
```

搜尋 JSON 錯誤回 400；欄位、狀態、query 長度／詞數、短詞或分頁錯誤回 422。搜尋字串不寫入日誌。

不提供 Embedding 或 MCP 端點。
