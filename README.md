# mf-mvp

A Go + MySQL backend for mutual fund NAV ingestion, persistence, and analytics.

## Features

- Mutual fund NAV ingestion from [mfapi.in](https://api.mfapi.in)
- MySQL persistence (time-series NAV data with incremental sync)
- Analytics engine — rolling returns, CAGR, max drawdown, percentiles
- Ranking system by performance (category + window aware)
- Rate limiter — 3-level sliding window (2/sec · 50/min · 300/hr)
- In-memory response cache (60s TTL, auto-flush on sync)
- Background sync every 6h + manual trigger via API

## Setup

**Prerequisites:** Go 1.21+, MySQL 8+

```bash
# 1. Create database and tables
mysql -u root -p < schema.sql

# 2. Set DSN (optional — defaults to root:root@127.0.0.1:3306/mfmvp)
export MF_DSN="user:pass@tcp(127.0.0.1:3306)/mfmvp?parseTime=true"

# 3. Install dependencies
go mod tidy

# 4. Run
go run main.go
```

## API

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/funds` | List all funds with latest NAV |
| `GET` | `/funds/{code}` | Fund detail + NAV history |
| `GET` | `/funds/{code}/analytics?window=1Y\|3Y\|5Y\|10Y` | Rolling returns, CAGR, drawdown |
| `GET` | `/funds/rank?category=&window=&sort_by=&limit=` | Ranked funds by analytics (`sort_by`: `median_return` or `max_drawdown`) |
| `POST` | `/sync/trigger` | Manually trigger incremental sync |
| `GET` | `/sync/status` | Current sync pipeline status |

## Tests

```bash
# Run all tests with race detector (DB tests skipped if MF_DSN is not set)
go test ./service/... -v -race

# Run with DB integration tests
MF_DSN="user:pass@tcp(127.0.0.1:3306)/mfmvp?parseTime=true" go test ./service/... -v -race
```

Covers: analytics engine (rolling returns, CAGR, drawdown, percentiles), rate limiter (concurrency + timing), and pipeline helpers (date conversion, incremental cutoff, idempotency).


## Notes

- First run fetches full NAV history from the API — may take 30–60s due to the rate limiter.
- A MySQL connection is required; the server will not start without one.
- Set `MF_DSN` before running if your MySQL credentials differ from the default.
