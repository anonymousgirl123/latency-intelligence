# Latency Intelligence Platform

A **Go-based observability backend** that transforms raw OpenTelemetry distributed traces into actionable latency intelligence — powering real-time p99 data in your IDE and automated regression gates in your CI/CD pipeline.

---

## Features

-  **OTLP gRPC Receiver** — ingests spans directly from any OpenTelemetry-compatible app or Collector
-  **Percentile Analytics** — computes p50 / p95 / p99 latency per method using ClickHouse `quantile()`
-  **Hotspot Detection** — surfaces the top-N slowest methods across any service and environment
-  **Regression Detection** — compares p99 between two commits and fails CI with HTTP 422 on regression
-  **IDE Integration** — REST endpoints consumed by the companion IntelliJ plugin for inline latency hints
-  **Docker-first** — single `docker compose up` spins up the full stack

---

##  Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        External Clients                         │
│   Instrumented App    IntelliJ Plugin    CI Pipeline      │
└──────┬──────────────────────┬───────────────────────┬──────────┘
       │ OTLP/gRPC :4317      │ GET /calibrate        │ POST /regression
       │ OTLP/HTTP :4318      │ GET /hotspots         │ POST /webhook/ci
       ▼                      ▼                       ▼
┌─────────────────┐   ┌──────────────────────────────────────────┐
│  OTel Collector │   │       Latency Intelligence (Go)          │
│  (optional fan- │──▶│                                          │
│  in proxy)      │   │  ┌─────────────┐   ┌─────────────────┐  │
│  :4318 HTTP     │   │  │ OTLP gRPC   │   │  HTTP REST API  │  │
│  :14250 Jaeger  │   │  │ Receiver    │   │  :8080 (go-chi) │  │
└─────────────────┘   │  └──────┬──────┘   └────────┬────────┘  │
                       │         │                    │           │
                       │         ▼                    ▼           │
                       │  ┌──────────────────────────────────┐   │
                       │  │       ClickHouseStore             │   │
                       │  │  InsertSpans · GetLatencyStats   │   │
                       │  │  GetTopHotspots · GetByCommit    │   │
                       │  └──────────────┬───────────────────┘   │
                       └─────────────────┼─────────────────────── ┘
                                         │
                    ┌────────────────────┴──────────────────┐
                    ▼                                        ▼
          ┌─────────────────┐                     ┌──────────────────┐
          │   ClickHouse    │                     │      Redis       │
          │   :9000         │                     │  :6379           │
          │  spans (90d)    │                     │  (cache planned) │
          │  hourly (365d)  │                     └──────────────────┘
          └─────────────────┘
```

---

##  Project Structure

```
latency-intelligence/
├── cmd/
│   └── server/
│       └── main.go              # Entry point — wires all components
├── internal/
│   ├── api/
│   │   ├── handler.go           # HTTP handlers (calibrate, hotspots, regression)
│   │   └── router.go            # go-chi router + middleware
│   ├── collector/
│   │   └── otlp.go              # OTLP gRPC receiver + span parser
│   ├── config/
│   │   └── config.go            # Env-based configuration loader
│   ├── regression/
│   │   └── detector.go          # p99 comparison between commits
│   └── store/
│       ├── clickhouse.go        # ClickHouse queries + auto-migration
│       └── models.go            # SpanRecord, LatencyStats, RegressionReport
├── docker/
│   ├── Dockerfile               # Multi-stage Go build
│   ├── docker-compose.yml       # Full stack: app + ClickHouse + Redis + OTel Collector
│   └── otel-collector-config.yaml
├── .env.example
├── go.mod
└── go.sum
```

---

##  API Reference

### `GET /health`
Liveness check.

```json
{ "status": "ok", "service": "latency-intelligence" }
```

---

### `GET /calibrate`
Returns real measured p50/p95/p99 for a specific method. Used by the IntelliJ plugin.

| Query Param | Required | Default | Description |
|---|---|---|---|
| `method` | ✅ | — | Fully qualified method name (e.g. `com.acme.OrderService.placeOrder`) |
| `service` | ✅ | — | Service name |
| `environment` | ❌ | `prod` | `prod` \| `staging` \| `dev` |
| `window` | ❌ | `24h` | Lookback duration (e.g. `1h`, `7d`) |

**Response `200 OK`:**
```json
{
  "method": "com.acme.OrderService.placeOrder",
  "service_name": "order-service",
  "environment": "prod",
  "p50_ms": 42.1,
  "p95_ms": 118.3,
  "p99_ms": 245.7,
  "min_ms": 11.2,
  "max_ms": 892.4,
  "sample_count": 14823,
  "window_start": "2026-05-19T10:00:00Z",
  "window_end":   "2026-05-20T10:00:00Z"
}
```

**Response `206 Partial Content`** (fewer than `MIN_SAMPLE_COUNT` spans):
```json
{
  "warning": "insufficient samples — estimates may be inaccurate",
  "sample_count": 12,
  "min_required": 30,
  "data": { ... }
}
```

---

### `GET /hotspots`
Returns the top-N slowest methods by p99. Used by the plugin dashboard and team views.

| Query Param | Required | Default | Description |
|---|---|---|---|
| `service` | ✅ | — | Service name |
| `environment` | ❌ | `prod` | Target environment |
| `limit` | ❌ | `10` | Number of results |
| `window` | ❌ | `24h` | Lookback duration |

**Response `200 OK`:**
```json
{
  "environment": "prod",
  "service": "order-service",
  "window": "24h0m0s",
  "count": 5,
  "hotspots": [ { ... }, { ... } ]
}
```

---

### `POST /regression`
Compares p99 latency between a baseline and candidate commit for a set of methods.  
Returns **HTTP 422** if any regression is detected — use this to **fail CI checks**.

**Request body:**
```json
{
  "environment":      "staging",
  "baseline_commit":  "abc1234",
  "candidate_commit": "def5678",
  "methods": [
    "com.acme.OrderService.placeOrder",
    "com.acme.PaymentService.charge"
  ]
}
```

**Response `200 OK`** (no regression):
```json
{
  "regression_count": 0,
  "total_checked": 2,
  "threshold_pct": 20,
  "reports": [
    {
      "method": "com.acme.OrderService.placeOrder",
      "baseline_p99_ms": 210.4,
      "candidate_p99_ms": 218.1,
      "delta_ms": 7.7,
      "delta_pct": 0.037,
      "is_regression": false
    }
  ]
}
```

**Response `422 Unprocessable Entity`** (regression found — fails CI):
```json
{
  "regression_count": 1,
  "total_checked": 2,
  "threshold_pct": 20,
  "reports": [
    {
      "method": "com.acme.PaymentService.charge",
      "baseline_p99_ms": 180.0,
      "candidate_p99_ms": 290.0,
      "delta_ms": 110.0,
      "delta_pct": 0.611,
      "is_regression": true
    }
  ]
}
```

---

### `POST /webhook/ci`
Identical to `POST /regression` — a dedicated path for CI pipeline webhook integration.

---

## ⚡ Getting Started

### Prerequisites
- [Docker](https://www.docker.com/) + Docker Compose
- Go 1.22+ (for local development only)

### 1. Clone & configure

```bash
git clone https://github.com/kamini/latency-intelligence.git
cd latency-intelligence
cp .env.example .env
# Edit .env as needed
```

### 2. Start the full stack

```bash
docker compose -f docker/docker-compose.yml up --build
```

This brings up:
| Service | Port(s) | Description |
|---|---|---|
| `latency-intelligence` | `8080`, `4317` | REST API + OTLP gRPC |
| `clickhouse` | `9000`, `8123` | Columnar DB (ClickHouse Play UI on `8123`) |
| `redis` | `6379` | Cache layer |
| `otel-collector` | `4318`, `14250` | OTLP/HTTP + Jaeger ingress |

### 3. Verify

```bash
curl http://localhost:8080/health
# {"status":"ok","service":"latency-intelligence"}
```

### 4. Run locally (without Docker)

```bash
# Ensure ClickHouse and Redis are running, then:
go run ./cmd/server
```

---

##  Configuration

All settings are read from environment variables (or a `.env` file at the project root).

| Variable | Default | Description |
|---|---|---|
| `HTTP_PORT` | `8080` | REST API port |
| `GRPC_PORT` | `4317` | OTLP gRPC receiver port |
| `CLICKHOUSE_ADDR` | `localhost:9000` | ClickHouse address |
| `CLICKHOUSE_DB` | `latency` | Database name |
| `CLICKHOUSE_USER` | `default` | ClickHouse user |
| `CLICKHOUSE_PASSWORD` | _(empty)_ | ClickHouse password |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `REDIS_PASSWORD` | _(empty)_ | Redis password |
| `REGRESSION_THRESHOLD_PCT` | `0.20` | Alert if p99 increases by more than this fraction (e.g. `0.20` = 20%) |
| `MIN_SAMPLE_COUNT` | `30` | Minimum spans required before reporting p99 |

---

##  Data Storage

### ClickHouse Tables

#### `spans` — raw trace data
```sql
CREATE TABLE spans (
    trace_id     String,
    span_id      String,
    method       String,      -- e.g. "com.acme.OrderService.placeOrder"
    file_path    String,      -- e.g. "com/acme/OrderService.java"
    call_type    String,      -- HTTP | DB | KAFKA | REDIS | SLEEP | INTERNAL
    duration_ms  Float64,
    environment  String,      -- prod | staging | dev
    commit_hash  String,
    service_name String,
    ts           DateTime
) ENGINE = MergeTree()
ORDER BY (environment, service_name, method, ts)
TTL ts + INTERVAL 90 DAY
```

#### `latency_hourly` — pre-aggregated percentiles
```sql
CREATE TABLE latency_hourly (
    hour         DateTime,
    method       String,
    environment  String,
    commit_hash  String,
    service_name String,
    p50_ms       Float64,
    p95_ms       Float64,
    p99_ms       Float64,
    min_ms       Float64,
    max_ms       Float64,
    sample_count UInt64
) ENGINE = SummingMergeTree()
ORDER BY (environment, service_name, method, commit_hash, hour)
TTL hour + INTERVAL 365 DAY
```

> **Schema migrations** are applied automatically on service startup — no manual DDL needed.

---

##  Regression Detection Logic

```
Δp99      = candidate_p99 - baseline_p99
Δp99 (%)  = Δp99 / baseline_p99

IsRegression = Δp99 (%) > REGRESSION_THRESHOLD_PCT
```

- Requires at least `MIN_SAMPLE_COUNT` (default: 30) spans for **both** baseline and candidate commits before evaluating — avoids false positives from sparse data.
- Returns `HTTP 422` when any regression is found, allowing CI pipelines to use HTTP status codes as a build gate.

---

##  OpenTelemetry Integration

Your app can send traces **directly** to the service on `:4317` (gRPC) or route through the bundled OpenTelemetry Collector.

### Span attribute mapping

| OTLP Attribute | Maps to |
|---|---|
| `service.name` | `service_name` |
| `deployment.environment` | `environment` |
| `git.commit.sha` | `commit_hash` |
| `code.namespace` + `code.function` | `method` (namespace.function) |
| `code.filepath` | `file_path` |
| `db.system` | triggers `call_type = DB` |
| `messaging.system = kafka` | triggers `call_type = KAFKA` |
| `db.system = redis` | triggers `call_type = REDIS` |
| `http.method` / `http.request.method` | triggers `call_type = HTTP` |

### Java agent example (`application.properties`)
```properties
otel.exporter.otlp.endpoint=http://localhost:4317
otel.exporter.otlp.protocol=grpc
otel.service.name=order-service
otel.resource.attributes=deployment.environment=staging,git.commit.sha=${GIT_COMMIT}
```

---

##  Tech Stack

| Component | Technology |
|---|---|
| Language | Go 1.22 |
| HTTP Router | [go-chi/chi v5](https://github.com/go-chi/chi) |
| Database | [ClickHouse](https://clickhouse.com/) (via `clickhouse-go/v2`) |
| Trace Protocol | [OpenTelemetry OTLP](https://opentelemetry.io/) over gRPC |
| gRPC | `google.golang.org/grpc` |
| Cache | Redis 7.4 |
| Container | Docker + Docker Compose |

---

##  Roadmap

- [ ] Redis caching for hot method→p99 lookups
- [ ] Materialized view auto-population from `spans` table
- [ ] Webhook signature verification for CI integrations
- [ ] Prometheus `/metrics` endpoint
- [ ] Multi-tenant service isolation

---

##  License

MIT

---

🌀 Magic applied with [Wibey VS Code Extension](https://wibey.walmart.com/code) 🪄
