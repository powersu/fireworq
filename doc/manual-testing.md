# Fireworq 手動測試指南

這份文件說明如何在本機手動啟動 Fireworq、送入 job、觀察 worker 收到請求、驗證 retry / 永久失敗行為,以及(選用)驗證 Redis dispatch 統計。目的是讓任何同事都能照著跑一輪端到端驗證。

所有指令預設在 repo 根目錄 `fireworq/` 執行。範例假設 server 綁在預設位址 `127.0.0.1:8080`。

---

## 測試的兩種層次

release 前建議兩層都跑過。兩者互補,缺一不可:

| 層次 | 這份文件的第 1~8 節<br>(功能 / 端到端測試) | `make test`<br>(自動化測試套件) |
| ---- | -------------------------------------------- | -------------------------------- |
| 性質 | 人當使用者,實際送 job、用眼睛觀察行為       | Go test framework 自動跑,斷言判 pass/fail |
| 驗證什麼 | 真實 server 的端到端行為、worker 互動、統計寫入 | 各 package 的 unit / integration 邏輯正確性 |
| 需要什麼 | 跑起來的 server + 自建 worker                | Go toolchain + MySQL(stats 用 miniredis,免真 Redis) |
| 何時跑 | 想確認某個功能實際跑起來對不對               | 每次改動後、push 前必跑           |

- **自動化測試怎麼手動跑** → 見下方「附錄 A:手動執行自動化測試(`make test`)」。
- **功能測試怎麼做** → 見第 1 節以後。

---

## 0. 名詞與資料流速覽

```
HTTP POST /job/{category}  →  依 routing 找到對應 queue  →  寫入 storage
dispatcher 輪詢  →  取出 job  →  POST 到 job.url(payload 當 body)  →  依 worker 回應決定 成功 / retry / 永久失敗
```

- **Queue**:實際存放與派送 job 的單位(有 polling interval、max workers 等設定)。
- **Routing**:把 job 的 `category` 對應到某個 queue 名稱。
- **Worker**:你自己的 HTTP 服務。Fireworq 會 POST 到 job 的 `url`,body 是 job 的 `payload`。

---

## 1. 前置準備

### 1-1. Build 執行檔

```bash
make build          # 產生 ./fireworq(含 race detector 的 DEBUG 版)
```

> 若剛跑過 `make clean`,`assets.go` / `mock_*.go` 會被刪掉,`make build` 會自動 `make generate` 重新產生,不需手動處理。

### 1-2. 選擇 driver

- **`in-memory`**:不需要任何外部服務,重啟即清空。**最適合快速手動測試**,本文件主線就用它。
- **`mysql`**:正式環境用的 driver,資料會落地。需要一個可連線的 MySQL(見 [CLAUDE.md](../CLAUDE.md) 的 podman 流程)。

---

## 2. 快速路徑(in-memory,不需 MySQL / Redis)

這一節可以在 3 分鐘內完成一輪完整驗證。建議開 **三個終端機**:A 跑 server、B 跑 worker、C 送指令。

### 2-1. 終端機 B:啟動一個測試 worker

Fireworq 會把 job POST 到這個 worker。以下用 Python 標準函式庫寫一個最小 worker,收到請求就印出內容並回 `success`:

```python
# save as test-worker.py, run:  python test-worker.py
from http.server import BaseHTTPRequestHandler, HTTPServer
import json

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(length).decode('utf-8')
        attempt = self.headers.get('X-Attempt')
        print(f"[worker] path={self.path} X-Attempt={attempt} body={body}", flush=True)

        # 回應必須是 JSON,status 為 success|failure|permanent-failure
        resp = json.dumps({"status": "success", "message": "ok"}).encode('utf-8')
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(resp)

    def log_message(self, *args):  # 靜音預設 access log
        pass

HTTPServer(('127.0.0.1', 9000), Handler).serve_forever()
```

```bash
python test-worker.py     # 監聽 127.0.0.1:9000
```

### 2-2. 終端機 A:啟動 Fireworq server

```bash
FIREWORQ_BIND=127.0.0.1:8080 \
FIREWORQ_DRIVER=in-memory \
./fireworq
```

看到啟動 log 且沒有 error 即代表就緒。

> 設定一律用 `FIREWORQ_` 前綴的環境變數,或對應的 CLI flag(底線換連字號,例如 `--driver`)。完整清單見 [doc/config.md](config.md)。

### 2-3. 終端機 C:建 queue → 建 routing → 送 job

以下用 `curl`。Windows PowerShell 使用者建議在 Git Bash 執行,或改用 `Invoke-RestMethod`(見第 6 節)。

**(1) 建立一個 queue,名為 `test_queue`:**

```bash
curl -X PUT http://127.0.0.1:8080/queue/test_queue \
  -H 'Content-Type: application/json' \
  -d '{"polling_interval": 200, "max_workers": 5}'
```

**(2) 建立 routing,把 category `greeting` 對應到 `test_queue`:**

```bash
curl -X PUT http://127.0.0.1:8080/routing/greeting \
  -H 'Content-Type: application/json' \
  -d '{"queue_name": "test_queue"}'
```

**(3) 送一個 job(category = `greeting`):**

```bash
curl -X POST http://127.0.0.1:8080/job/greeting \
  -H 'Content-Type: application/json' \
  -d '{
        "url": "http://127.0.0.1:9000/work",
        "payload": {"hello": "world"}
      }'
```

回應會是 `PushResult`,包含 `id`、`queue_name`。

**預期結果:** 幾百毫秒內,終端機 B 的 worker 會印出類似:

```
[worker] path=/work X-Attempt=1 body={"hello": "world"}
```

代表 job 已成功派送。因為 worker 回 `success`,job 會從 queue 刪除。

### 2-4. 驗證統計

```bash
curl http://127.0.0.1:8080/queue/test_queue/stats     # 該 queue 的累計統計
curl http://127.0.0.1:8080/stats                       # 全域統計(Prometheus 風格)
```

`test_queue/stats` 中 `total_pushes`、`total_pops`、`outstanding_jobs` 等數字應反映剛才送的 job。

---

## 3. 測試 retry 與永久失敗

改變 worker 的回應即可驗證失敗流程。把 `test-worker.py` 的 `resp` 那行換成不同 status:

| worker 回應 `status`     | Fireworq 行為                                              |
| ------------------------ | --------------------------------------------------------- |
| `success`                | 成功,刪除 job                                             |
| `failure`                | 失敗,若還有 retry 次數則排程重試,否則轉永久失敗          |
| `permanent-failure`      | 直接永久失敗,不再重試                                     |
| 非 2xx / 非法 JSON / timeout | 視為 failure(同上重試邏輯)                            |

> HTTP status code **不影響** 判定,判定只看 body 裡的 `status` 欄位。

### 3-1. 測試 retry

送 job 時帶上 retry 參數(單位:`retry_delay` 為秒):

```bash
curl -X POST http://127.0.0.1:8080/job/greeting \
  -H 'Content-Type: application/json' \
  -d '{
        "url": "http://127.0.0.1:9000/work",
        "payload": {"hello": "retry"},
        "max_retries": 3,
        "retry_delay": 2
      }'
```

把 worker 改成回 `{"status": "failure"}`,預期在 worker 端會看到 **同一個 job 被打多次**,`X-Attempt` 從 `1` 遞增到 `4`(1 次首發 + 3 次重試),每次間隔約 `retry_delay` 秒。

### 3-2. 觀察各狀態的 job

```bash
curl 'http://127.0.0.1:8080/queue/test_queue/waiting'    # 等待派送
curl 'http://127.0.0.1:8080/queue/test_queue/grabbed'    # 派送中
curl 'http://127.0.0.1:8080/queue/test_queue/deferred'   # 等待重試(延後)
curl 'http://127.0.0.1:8080/queue/test_queue/failed'     # 永久失敗紀錄
```

耗盡 retry 後,job 會出現在 `failed`。

### 3-3. 其他 job 參數

送 job 時可帶(皆為選填,單位見註解):

| 欄位          | 說明                          |
| ------------- | ----------------------------- |
| `run_after`   | 延後幾秒才可被派送            |
| `timeout`     | 對 worker 請求的逾時秒數      |
| `max_retries` | 最大重試次數                  |
| `retry_delay` | 每次重試間隔秒數              |
| `failure_url` | 失敗識別 URL(見第 4 節)     |

---

## 4. (選用)驗證 Redis dispatch 統計

這是選用功能:把 webhook 派送結果寫進 Redis 做失敗率監控。**注意:目前 `failure_url` 本身不會被實際呼叫**,它只被用來從 query 參數抽取 `org_id` 與 `target_env` 當作統計的識別 key(失敗回呼的 HTTP POST 已停用,見 [jobqueue/jobqueue.go](../jobqueue/jobqueue.go) 內註解)。

### 4-1. 啟動 Redis 並開啟功能

```bash
# 用 podman/docker 起一個 Redis
podman run -d --name test-redis -p 6379:6379 docker.io/library/redis:7

# 啟動 server 時加上 redis_addr
FIREWORQ_BIND=127.0.0.1:8080 \
FIREWORQ_DRIVER=in-memory \
FIREWORQ_REDIS_ADDR=127.0.0.1:6379 \
./fireworq
```

> `redis_addr` 為空 → 統計功能停用,所有統計呼叫皆為 no-op。連線失敗會在啟動時 panic(與 MySQL 行為一致)。

### 4-2. 送一個帶 `failure_url` 的 job,並讓它失敗

`failure_url` 的 query 需帶 `org_id` 與 `target_env`:

```bash
curl -X POST http://127.0.0.1:8080/job/greeting \
  -H 'Content-Type: application/json' \
  -d '{
        "url": "http://127.0.0.1:9000/work",
        "payload": {"hello": "stats"},
        "max_retries": 0,
        "failure_url": "http://example.com/cb?org_id=org123&target_env=staging"
      }'
```

把 worker 改回 `{"status": "permanent-failure"}`(或 `failure` 且 `max_retries: 0`),讓 job 落入永久失敗。

### 4-3. 檢查 Redis key

```bash
podman exec -it test-redis redis-cli

# 依 target_env=staging、org_id=org123 查對應 bucket(時間戳依當下而定)
KEYS webhook:staging:stats:org123:*
HGETALL webhook:staging:stats:org123:1d:<YYYYMMDD>   # 例如 1d:20260712
ZRANGE  webhook:staging:active_subscribes 0 -1 WITHSCORES
```

**預期:** Hash 內 `total`、`fail`、`permanent_fail` 各 +1(成功則是 `total`、`success` +1)。Sorted set `active_subscribes` 會有 `org123` 這個 member。

> Key 結構與分類規則詳見 [CLAUDE.md](../CLAUDE.md) 的「Dispatch Statistics (Redis)」章節。
> 沒帶 `org_id` / `target_env` 的 job 會靜默略過統計,不會報錯。

---

## 5. MySQL driver 的完整測試

要驗證正式 driver(資料落地、queue 定義持久化),把 driver 換成 `mysql` 並提供 DSN:

```bash
FIREWORQ_BIND=127.0.0.1:8080 \
FIREWORQ_DRIVER=mysql \
FIREWORQ_MYSQL_DSN='nobody:nobody@tcp(127.0.0.1:3306)/fireworq' \
./fireworq
```

MySQL 的啟動方式(podman pod + `mysql:8.4`)見 [CLAUDE.md](../CLAUDE.md) 的「Running Tests via Podman」。第 2~4 節的所有 API 操作完全相同,差別在於 queue 定義與 job 會存進 MySQL,重啟 server 後 queue / routing 仍存在。

驗證持久化:建好 queue、送幾個 job 後重啟 server,`GET /queues`、`GET /routings` 應仍回傳先前建立的定義。

---

## 6. API 速查表

| 方法 & 路徑                              | 用途                          |
| ---------------------------------------- | ----------------------------- |
| `POST /job/{category}`                   | 送一個 job                    |
| `PUT /queue/{name}`                      | 建立 / 更新 queue             |
| `GET /queue/{name}`                      | 查 queue 定義                 |
| `DELETE /queue/{name}`                   | 刪除 queue                    |
| `GET /queues`                            | 列出所有 queue                |
| `GET /queue/{name}/stats`                | queue 統計                    |
| `GET /queue/{name}/{waiting|grabbed|deferred|failed}` | 查各狀態 job     |
| `PUT /routing/{category}`                | 建立 / 更新 routing           |
| `GET /routings`                          | 列出所有 routing              |
| `DELETE /routing/{category}`             | 刪除 routing                  |
| `GET /stats`                             | 全域統計                      |
| `GET /settings`                          | 目前生效的設定                |
| `GET /version`                           | 版本資訊                      |

### PowerShell(Windows)等價寫法

在 Windows 原生 PowerShell 沒有 `curl` 時,可用 `Invoke-RestMethod`:

```powershell
# 建 queue
Invoke-RestMethod -Method Put -Uri http://127.0.0.1:8080/queue/test_queue `
  -ContentType 'application/json' `
  -Body '{"polling_interval": 200, "max_workers": 5}'

# 送 job
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/job/greeting `
  -ContentType 'application/json' `
  -Body '{"url":"http://127.0.0.1:9000/work","payload":{"hello":"world"}}'
```

---

## 7. 清理

```bash
# 停掉 server / worker:各終端機 Ctrl+C
podman rm -f test-redis            # 若有起 Redis
# MySQL pod 的清理見 CLAUDE.md
```

---

## 8. 常見問題排查

| 症狀                                   | 可能原因 / 檢查方向                                            |
| -------------------------------------- | ------------------------------------------------------------- |
| 送 job 回 `404` / category 錯誤        | 該 category 沒有對應 routing,且未設定 `queue_default`         |
| worker 收不到請求                      | job 的 `url` 打錯、worker 沒起、queue 的 `max_workers` 為 0    |
| job 一直重試不停                       | worker 回的不是合法 JSON,或 `status` 不是三個合法值之一       |
| 改了 queue 設定沒生效                  | 需用 `PUT /queue/{name}` 更新;service 會偵測 revision 變化重載 |
| Redis 沒有任何 key                     | `failure_url` 沒帶 `org_id`/`target_env`,或 job 尚未進入失敗流程 |
| server 啟動即 panic                    | MySQL 或 Redis 連不上(啟動時強制連線)                         |

---

## 附錄 A:手動執行自動化測試(`make test`)

`make test` 會跑整個 Go 測試套件,自動對 **`mysql`** 與 **`in-memory`** 兩個 driver 各跑一遍(靠 `test.RunAll()`),含 race detector,並輸出 JUnit 報告。stats 相關測試用 in-process 的 **miniredis**,因此**不需要真的 Redis**;但 MySQL 是必要的。

### A-1. 方式一:podman(推薦,不需本機裝 Go / MySQL)

即 [CLAUDE.md](../CLAUDE.md) 的「Running Tests via Podman」。第一次要建 pod 並起 MySQL:

```bash
# 1. 建 pod + 起 MySQL(只有第一次或清掉後才需要)
podman pod create --name fireworq-test -p 3306
podman run -d --pod fireworq-test --name test-mysql \
  -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=fireworq \
  -e MYSQL_USER=nobody -e MYSQL_PASSWORD=nobody \
  docker.io/library/mysql:8.4 --mysql-native-password=ON

# 2. 等 MySQL ready
for i in $(seq 1 30); do podman exec test-mysql mysqladmin ping -h localhost -u nobody -pnobody --silent 2>/dev/null && echo "MySQL ready" && break; echo "Waiting... ($i)"; sleep 2; done

# 3. 在 golang 容器裡跑 generate + test
podman run --rm --pod fireworq-test \
  -v "d:/htdocs/CloudRecording/vmx-fireworq/fireworq://workspace" \
  -w //workspace \
  -e "FIREWORQ_MYSQL_DSN=nobody:nobody@tcp(localhost:3306)/fireworq" \
  docker.io/library/golang:1.26 \
  bash -c "git config --global --add safe.directory /workspace && make generate && make test"
```

- pod 可重複使用。之後只要 `podman pod start fireworq-test`,再跑第 3 步即可。
- 只想看 PASS/FAIL 摘要:在 `bash -c` 命令尾接 `2>&1 | grep -E '(^ok|^FAIL|build failed|PASS$|FAIL$)'`。
- pod 設定要改動時才清掉:`podman pod rm -f fireworq-test`。

### A-2. 方式二:本機直接跑(已裝 Go + 有可連的 MySQL)

```bash
export FIREWORQ_MYSQL_DSN='nobody:nobody@tcp(127.0.0.1:3306)/fireworq'
make generate     # 產生 assets.go / mock_*.go(clean 後必做)
make test         # 兩個 driver + race + JUnit 輸出
```

### A-3. 常用變化

```bash
# 只跑單一測試(快速迭代)
go test -run TestJobQueueDecorator_Success -v ./stats

# 只跑某個 package
go test -v ./jobqueue/...

# 產生覆蓋率報告
make cover
```

### A-4. push 前必跑:lint

```bash
make clean lint      # golint + go vet + gofmt -s
```

> **重要:** CI 會用 `gofmt -s` 卡關,而 `go test` 抓不到格式問題。push 前務必跑過 `make clean lint`(或至少 `gofmt -d -s ./...`),否則 CI 會因格式失敗。
