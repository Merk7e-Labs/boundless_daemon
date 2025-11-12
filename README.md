Docker Log Aggregator
=====================

This repo contains a tiny pipeline for exercising log processing code locally:

- `aggregator.go` tails a log file, extracts `Completed order` entries with a specific hex prefix, deduplicates the IDs across runs, and POSTs the cumulative results to a reporting endpoint.
- `mock_server.go` exposes that reporting endpoint plus a simple HTML view so you can see what the aggregator has collected.
- `test.txt` is a captured snippet from `docker compose logs broker2 -f` (tail of `broker2`) that you can use as an offline data source.

Files & Responsibilities
------------------------

### `aggregator.go`
* Watches a log file in either time-based mode (default, parses RFC3339/RFC3339Nano timestamps anywhere in each line) or offset mode (tail-like if timestamps are missing).
* Filters order IDs by prefix (default `0x5a1f4d397`), deduplicates across runs using `agg_state.json`, keeps a cumulative count, and POSTs JSON to the mock server's `/report` endpoint.
* Streams `docker compose logs <broker>` output (default `broker2`) at a configurable interval and POSTs batches to the dashboard backend so operators can inspect broker logs remotely.
* Supports `-reset` to drop the saved state and reprocess the entire log.

### `mock_server.go`
* Lightweight HTTP server on `:9001` with:
  - `POST /report` for updates from the aggregator.
  - `GET /report` to fetch the latest JSON payload.
  - `GET /` rendering the latest payload in HTML.
  - `POST /reset` to clear in-memory state.
* Normalizes casing, stores the time the payload was received, and guards concurrent access with a mutex.

### `test.txt`
* Reference log file committed to the repo so you can run the aggregator without needing Docker or the real broker.
* Contains timestamped lines copied from `docker compose logs broker2 -f --tail 50000 | grep "Completed order"`.

Running the Mock Server
-----------------------

```bash
go run mock_server.go
```

The server logs `Mock server listening on :9001`. Leave it running if you want to test locally—run the aggregator with `-url=http://localhost:9001/report` to target the mock instead of the real backend.

Running the Aggregator
----------------------

Quick start (defaults baked into the binary). The scraper now stays running and re-processes the log every 15 minutes, posting updated totals to the backend after each scan (runs are aligned to real UTC quarter-hours: 00, 15, 30, 45):

```bash
go run boundless_scraper.go
```

The defaults can be overridden with flags or environment variables (see below).

Default (time-based) mode:

```bash
go run aggregator.go \
  -file=./test.txt \
  -prefix=0x5a1f4d397 \
  -prover=0x6220892679110898abd78847d6f0a639e3408dc7 \
  -url=http://localhost:9090/api/offchain/report \
  -state=./agg_state.json
```

Offset (tail-style) mode for logs that lack timestamps:

```bash
go run aggregator.go \
  -file=./test.txt \
  -prefix=0x5a1f4d397 \
  -prover=0x6220892679110898abd78847d6f0a639e3408dc7 \
  -url=http://localhost:9090/api/offchain/report \
  -state=./agg_state.json \
  -mode=offset
```

Environment overrides:

| Env var                 | Default value                               |
| ----------------------- | ------------------------------------------- |
| `SCRAPER_FILE`          | `./test.txt`                                |
| `SCRAPER_PREFIX`        | `0x5a1f4d397`                               |
| `BOUNDLESS_PROVER_ADDR` | `0x6220892679110898abd78847d6f0a639e3408dc7` |
| `BOUNDLESS_REPORT_URL`  | `http://localhost:9090/api/offchain/report` |
| `SCRAPER_STATE_FILE`    | `./agg_state.json`                          |
| `SCRAPER_INTERVAL`      | `15m` (aligned to UTC bucket boundaries)    |
| `SCRAPER_BROKER`        | `broker2`                                   |
| `SCRAPER_LOG_INTERVAL`  | `10s`                                       |
| `BOUNDLESS_LOG_ENDPOINT`| `http://localhost:9090/api/logs`            |

Add `-reset` if you want to discard `agg_state.json` and start from scratch. When the run finishes you'll see a summary such as:

```
Mode=time | New IDs this run: 3 | Cumulative: 12 | LastReadAt=2025-11-04T18:31:18Z | LastOffset=0
```

Viewing Results
---------------

Open a browser to `http://localhost:9001/` to see the formatted list of order IDs, or `curl http://localhost:9001/report` for the raw JSON. Use `curl -X POST http://localhost:9001/reset` if you need to clear the server-side copy without touching `agg_state.json`.
