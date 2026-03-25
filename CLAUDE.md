# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Fireworq is a lightweight, high-performance job queue system written in Go. It accepts jobs via HTTP API and dispatches them to workers via HTTP POST. Built on MySQL for persistence (with an in-memory driver for development/testing).

## Build & Development Commands

```bash
make build                              # Build binary (with race detector, DEBUG prerelease)
make test                               # Run all tests (both drivers, race detection, JUnit output)
make cover                              # Generate coverage report
make clean lint                         # Clean generated files, then run golint + go vet + gofmt -s
make generate                           # Run go:generate (assets + mocks)
make release PRERELEASE=                # Build release binary (CGO_ENABLED=0, no race detector)
```

Tests require MySQL. Set `FIREWORQ_MYSQL_DSN` env var (e.g., `user:password@tcp(host:port)/database`). Tests run against both `mysql` and `in-memory` drivers automatically via `test.RunAll()`.

Run a single test: `go test -run TestName -v ./path/to/package`

**Important:** Always run `make clean lint` (or at minimum `gofmt -d -s ./...`) before pushing. CI runs `gofmt -s` as part of `make lint` and will fail on formatting issues that `go test` alone does not catch.

Docker development: `script/docker/compose up` (builds and runs with MySQL). Use `script/docker/compose clean` when dependencies change.

### Running Tests via Podman (Local)

Uses a self-contained podman pod with MySQL and the official Go image. No pre-existing containers required.

**1. Create pod and start MySQL:**

```bash
podman pod create --name fireworq-test -p 3306
podman run -d --pod fireworq-test --name test-mysql \
  -e MYSQL_ROOT_PASSWORD=root \
  -e MYSQL_DATABASE=fireworq \
  -e MYSQL_USER=nobody \
  -e MYSQL_PASSWORD=nobody \
  docker.io/library/mysql:8.0
```

**2. Wait for MySQL to be ready:**

```bash
for i in $(seq 1 30); do podman exec test-mysql mysqladmin ping -h localhost -u nobody -pnobody --silent 2>/dev/null && echo "MySQL ready" && break; echo "Waiting... ($i)"; sleep 2; done
```

**3. Run full test suite:**

```bash
podman run --rm --pod fireworq-test \
  -v "d:/htdocs/CloudRecording/vmx-fireworq/fireworq://workspace" \
  -w //workspace \
  -e "FIREWORQ_MYSQL_DSN=nobody:nobody@tcp(localhost:3306)/fireworq" \
  docker.io/library/golang:1.25 \
  bash -c "git config --global --add safe.directory /workspace && make generate && make test"
```

Summary only (shows PASS/FAIL per package): append `2>&1 | grep -E '(^ok|^FAIL|build failed|PASS$|FAIL$)'` to the `bash -c` command.

**4. Re-run tests (pod already exists):**

The pod can be reused. If it was stopped, start it first:

```bash
podman pod start fireworq-test
```

Then run step 3 again.

**5. Clean up (optional, only when pod config needs to change):**

```bash
podman pod rm -f fireworq-test
```

## Architecture

### Request Flow

```
HTTP POST /job/{category} → Service.Push() → RoutingRepository (resolve category→queue)
    → JobQueue.Push() → Storage (MySQL table or in-memory heap)

Dispatcher polling loop → JobQueue.Pop() → job buffer (channel)
    → worker semaphore → HTTPWorker.Work() → POST to job.URL with payload
    → JobQueue.Complete() → success: delete | failure: retry or log
```

### Key Packages

- **`config/`** - Config resolution: CLI flags > env vars (prefixed `FIREWORQ_`) > defaults. Keys use underscores internally, hyphens on CLI (`queue_default` → `--queue-default`).
- **`jobqueue/`** - Core job queue interface. Two implementations: `jobqueue/mysql/` (production, primary/backup support) and `jobqueue/inmemory/` (heap-based, for dev/test).
- **`dispatcher/`** - Per-queue dispatch loop. Contains `kicker/` (polling strategy) and `worker/` (HTTP POST execution). Controls concurrency via semaphore channel and rate limiting via `golang.org/x/time/rate`.
- **`repository/`** - Metadata persistence (queue definitions, routing rules). Factory pattern selects MySQL or in-memory based on `driver` config. Uses revision numbers for change detection.
- **`service/`** - Orchestration layer. Manages running queues (JobQueue + Dispatcher pairs), config watchers that poll for queue/routing changes, and lifecycle (start/stop/reload).
- **`web/`** - REST API via gorilla/mux. Custom `handler` type returns errors (auto-mapped to client 4xx or server 5xx). Response buffering for error handling.
- **`model/`** - Data types: `Queue` (name, polling_interval, max_workers, throttle settings) and `Routing` (job category → queue name).
- **`data/`** - SQL schemas and query templates, embedded into binary via `go-assets-builder`.
- **`log/`** - File writer with `Reopen()` for log rotation (triggered by SIGUSR1).
- **`test/`** - Test utilities. `test.RunAll()` runs tests against all drivers. `test.If("driver", "mysql")` for driver-conditional tests.

### Code Generation

`go:generate` is used extensively (run via `make generate`):
- `go-assets-builder` embeds LICENSE, AUTHORS, CREDITS, SQL files into `assets.go` files
- `mockgen` generates test mocks in `*_test.go` files (web package mocks Service, repositories, jobqueue interfaces)

Generated files: `assets.go` and `mock_*.go` — these are `.gitignore`d and must be regenerated after `make clean`.

### Worker Protocol

Workers receive `POST` with JSON payload body. Headers include `X-Attempt` (1-based). Workers respond with JSON `{"status":"success|failure|permanent-failure", "message":"..."}`. HTTP status code is ignored.

### Dispatch Statistics (Redis)

Optional feature that records webhook delivery results to Redis for failure rate monitoring. Enabled by setting `FIREWORQ_REDIS_ADDR`.

**Packages:**
- **`redisclient/`** - Global Redis client lifecycle (`Init()`, `Close()`, `IsEnabled()`). Uses `github.com/redis/go-redis/v9`. Connection failure at startup causes panic (same as MySQL). When `redis_addr` config is empty, feature is disabled and all stats calls are no-ops.
- **`stats/`** - Dispatch statistics collection via JobQueue decorator pattern:
  - `extractor.go` - Extracts `org_id` and `target_env` from `job.FailureURL()` query parameters
  - `writer.go` - `StatsWriter.RecordDispatch()` writes to Redis via pipeline (single round-trip)
  - `jobqueue.go` - `JobQueue` decorator wraps `jobqueue.JobQueue`, intercepts `Complete()` to record stats asynchronously (goroutine). Injected at `service/running_queue.go:startJobQueue()`.

**Redis Key Structure:**
- 5-min bucket (Hash): `webhook:{target_env}:stats:{org_id}:5m:{YYYYMMDDHHmm}` (minute truncated to 5-min boundary), TTL 2h
- 1-hour bucket (Hash): `webhook:{target_env}:stats:{org_id}:1h:{YYYYMMDDHH}`, TTL 25h
- Daily bucket (Hash): `webhook:{target_env}:stats:{org_id}:1d:{YYYYMMDD}`, TTL 7d
- Hash fields: `total`, `success`, `fail`, `permanent_fail`
- Active subscribers (Sorted Set): `webhook:{target_env}:active_subscribes`, score = Unix timestamp, member = org_id

**Result Classification (mirrors `jobqueue.(*jobQueue).Complete`):**
- success → `total+1, success+1`
- permanent fail (explicit or retries exhausted) → `total+1, fail+1, permanent_fail+1`
- retryable fail → `total+1, fail+1`

**Design Principles:**
- Redis write failures only log errors, never block dispatch flow
- No `org_id` or `target_env` in `FailureURL` → silently skip stats
- Async goroutine with 2-second context timeout per write

## Coding Conventions

- Use `golint`, `go vet`, `gofmt -s` (enforced by `make lint`)
- `scopelint` checks for variable capture issues in closures
- Version managed in `version.go`, follows semver
- Interface-heavy design: prefer interfaces for testability (factory pattern for drivers, strategy pattern for kicker/worker)
