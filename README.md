# Battery Cabin Operations Platform

A Go backend for the Ejina Banner microgrid energy-storage battery cabin
operations and maintenance scheduling platform.  It coordinates five parties —
duty dispatcher, inspection team leader, battery cabin equipment, maintenance
engineer, and local supply station — through inspection, alarm, repair,
inventory, and grid-switching workflows.

## Features

- **Weekly inspection dispatch** — team leaders create inspection orders; inspectors register temperature, humidity, and voltage readings per cabin.
- **Alarm reporting with duplicate suppression** — when two inspectors report the same cabin simultaneously, only the first order is kept; subsequent reporters are prompted to confirm.
- **Cabin locking & idempotent dispatch** — dispatching a maintenance order locks the cabin to prevent duplicate dispatch.
- **Dual acceptance** — repair closure requires sign-off from both the inspection team leader and the supply-station attendant.
- **Auto-transfer on timeout** — if the primary engineer does not arrive within the configured timeout (default 30 min), the order is auto-transferred to the backup engineer with SMS notification to both.
- **Inventory replenishment** — when spare-part stock drops below the safety line, a replenishment request is auto-generated (idempotent).
- **Dual-confirmation operations** — grid connection and black-start switching require confirmation from both the dispatcher and the supply station before execution.
- **Full audit trail** — every state transition is recorded for traceability.

## Project Layout

```
cmd/server/main.go              — HTTP server entry point
internal/domain/                — domain models & state machines (cabin, work order, inventory, operation)
internal/store/                 — concurrency-safe in-memory persistence
internal/service/               — application orchestration & business logic
internal/scheduler/             — background tasks (auto-transfer, inventory scan)
internal/httpapi/               — REST HTTP handlers
internal/notify/                — SMS notifier (in-memory mock)
```

## Running Locally

```bash
go run ./cmd/server
```

The server listens on port **51219**.  Configuration is read from `config.json`
(falls back to defaults if absent).

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET  | `/healthz` | Health check |
| POST | `/api/cabins` | Register a battery cabin |
| GET  | `/api/cabins` | List all cabins |
| POST | `/api/cabins/{id}/readings` | Submit temperature/humidity/voltage readings |
| POST | `/api/cabins/{id}/alarm` | Report an alarm (temp over-limit or insulation) |
| POST | `/api/workorders/inspection` | Dispatch an inspection order (weekly plan) |
| POST | `/api/workorders/inspection/{id}/start` | Start an inspection |
| POST | `/api/workorders/inspection/{id}/readings` | Record readings during inspection |
| POST | `/api/workorders/inspection/{id}/complete` | Complete an inspection |
| POST | `/api/workorders/maintenance/{id}/dispatch` | Dispatch a maintenance order (locks cabin) |
| POST | `/api/workorders/maintenance/{id}/arrive` | Engineer arrival on site |
| POST | `/api/workorders/maintenance/{id}/complete` | Engineer completes processing |
| POST | `/api/workorders/maintenance/{id}/accept` | Dual acceptance (leader + station) |
| POST | `/api/workorders/maintenance/{id}/transfer` | Manually transfer to backup engineer |
| GET  | `/api/workorders` | List all work orders |
| POST | `/api/inventory/parts` | Add a spare part |
| GET  | `/api/inventory/parts` | List spare parts |
| POST | `/api/inventory/parts/{id}/consume` | Consume stock (triggers replenishment if below safety line) |
| GET  | `/api/inventory/replenishments` | List replenishment requests |
| POST | `/api/operations` | Initiate grid-connect or black-start operation |
| POST | `/api/operations/{id}/confirm` | Supply-station confirmation |
| POST | `/api/operations/{id}/execute` | Execute a dual-confirmed operation |
| POST | `/api/operations/{id}/cancel` | Cancel a pending operation |
| GET  | `/api/operations` | List all operations |

## Testing

```bash
go test -timeout=120s -count=1 ./...
```

## Docker

Build the image (supports amd64 and arm64 via BuildKit):

```bash
docker build -t battery-ops .
```

Run the container:

```bash
docker run -p 51219:51219 battery-ops
```

Multi-architecture build:

```bash
docker buildx build --platform linux/amd64,linux/arm64 -t battery-ops .
```
