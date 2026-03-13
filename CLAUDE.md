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

Docker development: `script/docker/compose up` (builds and runs with MySQL). Use `script/docker/compose clean` when dependencies change.

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

## Coding Conventions

- Use `golint`, `go vet`, `gofmt -s` (enforced by `make lint`)
- `scopelint` checks for variable capture issues in closures
- Version managed in `version.go`, follows semver
- Interface-heavy design: prefer interfaces for testability (factory pattern for drivers, strategy pattern for kicker/worker)
