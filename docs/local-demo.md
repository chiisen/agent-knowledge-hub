# 本機示範

這份文件示範同一台電腦上的知識生命週期。它使用獨立的 `data/demo.db` 與 `127.0.0.1:18080`，不會改到預設的 `data/knowledge.db`，也不占用常用的 `8080`。

這不是自動驗收。自動驗收仍執行 `pwsh -NoProfile -File scripts/memory-smoke.ps1`。契約細節見 [api.md](api.md)。

## 範圍

示範會看到這些結果：

- `/healthz` 回 200，且不需要 Token。
- 新增的知識是 `draft`，`ETag` 為 `"1"`。
- 預設列表只回 `confirmed`，所以這時看不到草稿。
- 帶 `If-Match: "1"` 確認後，version 變成 2，`confirmed_by` 是 Token 名稱 `demo`。
- 搜尋「交易短小」命中這筆；搜尋「交易」回 422。
- 寫入 `project=other` 回 403，不會寫入。
- 帶 `If-Match: "2"` 封存後，單筆讀取回 404，搜尋不再命中。

服務只聽 loopback。別台電腦連不到這個位址。

## 準備

在專案根目錄操作。需要 Go，以及系統的 `python3` 與 `curl`。開啟兩個終端。

明文 Token 只出現在核發時的 stdout。不要把 `token` 欄位貼進對話、文件或版本控制。下面的指令把明文留在目前 shell 的 `TOKEN` 變數，不印出來。

## 終端 A：啟動

```sh
go run ./cmd/knowledge-hub -addr 127.0.0.1:18080 -db data/demo.db
```

看到服務開始寫日誌後，保留這個終端。結束時按 Ctrl+C，等待程序退出。

## 終端 B：操作

先確認服務可達：

```sh
curl -sS http://127.0.0.1:18080/healthz
```

預期輸出：

```json
{"status":"ok"}
```

核發 Token。這行會印出完整 JSON，其中包含明文；確認終端沒有被記錄後再繼續。

```sh
ISSUE=$(go run ./cmd/knowledge-hub token issue \
  -db data/demo.db \
  -name demo \
  -project demo)
TOKEN=$(printf '%s' "$ISSUE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"], end="")')
test -n "$TOKEN"
```

`test` 成功時沒有輸出，只代表變數不是空的。

新增草稿：

```sh
curl -sS -D - -o /tmp/knowledge-demo-create.json \
  http://127.0.0.1:18080/api/v1/knowledge \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"title":"SQLite 寫入原則","content":"保持交易短小，避免長鎖。","type":"convention","scope":"project","project":"demo","source":"本機示範"}'
```

預期狀態行是 `HTTP/1.1 201 Created`，其中一列是 `Etag: "1"`。正文裡 `status` 是 `draft`，`version` 是 1，`usable_as_rule` 是 false，`confirmed_by` 是 null。

```sh
python3 -c 'import json; d=json.load(open("/tmp/knowledge-demo-create.json"))["data"]; print(d["id"], d["status"], d["version"], d["usable_as_rule"])'
```

預設列表看不到草稿：

```sh
curl -sS 'http://127.0.0.1:18080/api/v1/knowledge?project=demo' \
  -H "Authorization: Bearer $TOKEN"
```

預期 `data` 是空陣列。指定草稿狀態才看得到：

```sh
curl -sS 'http://127.0.0.1:18080/api/v1/knowledge?status=draft&project=demo' \
  -H "Authorization: Bearer $TOKEN"
```

確認。把 `<id>` 換成上一筆印出的 UUID：

```sh
curl -sS -D - -o /tmp/knowledge-demo-confirm.json \
  -X PATCH "http://127.0.0.1:18080/api/v1/knowledge/<id>" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'If-Match: "1"' \
  -d '{"status":"confirmed"}'
```

預期 `HTTP/1.1 200 OK`。正文裡 `status` 是 `confirmed`，`version` 是 2，`confirmed_by` 是 `demo`，`usable_as_rule` 是 true。

搜尋至少三個 Unicode 字元：

```sh
curl -sS http://127.0.0.1:18080/api/v1/knowledge/search \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"query":"交易短小","project":"demo"}'
```

預期 `data` 含剛才的 id，`status` 是 `confirmed`。搜尋結果沒有 `confirmed_by`。

兩字搜尋會被拒絕：

```sh
curl -sS -D - http://127.0.0.1:18080/api/v1/knowledge/search \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"query":"交易","project":"demo"}'
```

預期 `HTTP/1.1 422 Unprocessable Entity`，`error.code` 是 `validation_failed`。

白名單外的 project 不能新增：

```sh
curl -sS -D - http://127.0.0.1:18080/api/v1/knowledge \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"title":"越權","content":"不該寫入","type":"convention","scope":"project","project":"other","source":"本機示範"}'
```

預期 `HTTP/1.1 403 Forbidden`，`error.code` 是 `forbidden`。

封存。ETag 用確認後的版本：

```sh
curl -sS -D - -o /dev/null \
  -X DELETE "http://127.0.0.1:18080/api/v1/knowledge/<id>" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'If-Match: "2"'
```

預期 `HTTP/1.1 204 No Content`，沒有 JSON 正文。再讀取同一筆：

```sh
curl -sS -D - "http://127.0.0.1:18080/api/v1/knowledge/<id>" \
  -H "Authorization: Bearer $TOKEN"
```

預期 `HTTP/1.1 404 Not Found`。再用「交易短小」搜尋，預期 `data` 是空陣列。

## 結束

在終端 A 按 Ctrl+C，等待程序退出。`data/demo.db` 與它的 WAL、SHM 可以保留，也可以在服務停止後刪除。這些檔案已被 `.gitignore` 排除。

若明文已經出現在不該保存的地方，輪替這把 Token。新的明文一樣不要貼出來：

```sh
go run ./cmd/knowledge-hub token rotate \
  -db data/demo.db \
  -id <核發 JSON 裡的 id> \
  -project demo
```

舊 Token 會在輪替當下撤銷。之後用舊 Token 呼叫 Knowledge API 會回 401 `unauthorized`。
