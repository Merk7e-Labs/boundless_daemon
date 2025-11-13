# Boundless Scraper

Go-based scraper that reads docker compose logs for the configured broker
service, counts `Completed order` entries since the last processed timestamp,
calculates the total number of cycles (0.01 trillion cycles per order), and
POSTs the metrics to a remote endpoint.

The scraper keeps its last processed timestamp in a JSON state file so that it
can resume after restarts without double counting.

## Project layout

```
.
├── go.mod                     # Go module definition
├── main.go                    # Entry point / scheduler loop
├── config.go                  # .env loading and configuration helpers
├── scraper.go                 # Log parsing, HTTP posting, command runner
├── state.go                   # Persistent timestamp storage helpers
├── .env.example               # Sample configuration
└── .vscode/launch.json        # VS Code debug configuration
```

## Requirements

- Go 1.22+
- Docker CLI available on the host (for running the configured log command)

## Configuration

Copy `.env.example` to `.env` and adjust the values as needed:

```bash
cp .env.example .env
```

| Variable                   | Description                                                                                  | Default                                       |
|----------------------------|----------------------------------------------------------------------------------------------|-----------------------------------------------|
| `SCRAPER_ENDPOINT`         | **Required.** HTTP endpoint that receives the JSON payload.                                   | –                                             |
| `SCRAPER_SERVICE`          | Docker compose service name to read logs from (e.g. `broker`, `broker2`, `broker3`).         | `broker`                                      |
| `SCRAPER_LOG_COMMAND`      | Command template used to fetch logs. `{service}` and `{since}` placeholders are supported.   | `docker compose logs {service} --since {since} --no-color` |
| `SCRAPER_WORKDIR`          | Directory from which the log command is executed (e.g. `/home/ubuntu/boundless`).            | `~/boundless`                                 |
| `SCRAPER_STATE_FILE`       | Path to the JSON file storing the last processed timestamp.                                   | `scraper_state.json`                          |
| `SCRAPER_INTERVAL`         | Interval between scrapes (Go duration string, e.g. `60s`, `5m`).                             | `60s`                                         |
| `SCRAPER_INITIAL_LOOKBACK` | How far back to look on the first run if no state exists (e.g. `10m`, `1h`).                  | `10m`                                         |
| `SCRAPER_COMMAND_TIMEOUT`  | Timeout for the docker log command (Go duration string).                                     | `60s`                                         |
| `SCRAPER_POST_TIMEOUT`     | Timeout for posting metrics to the remote endpoint.                                          | `15s`                                         |

## Running the scraper

export SCRAPER_ENDPOINT=http://localhost:8080/mock   # swap to real endpoint later
export SCRAPER_WORKDIR=~/boundless                   # wherever docker-compose lives
export SCRAPER_SERVICE=broker2                       # match running service name
export SCRAPER_STATE_FILE=~/scraper_state.json       # or /tmp/…
export SCRAPER_PROVER_ID=<your prover id>            #change this to the prover id of your actual prover

Run in one-off mode for debugging:

```bash
go run . -once
```

Run continuously (default behaviour):

```bash
go run .
```

### JSON payload example

```
{
  "orders_completed": 50,
  "total_cycles_trillions": 0.5,
  "window_start": "2025-11-07T05:00:00Z",
  "window_end": "2025-11-07T05:40:21Z",
  "service": "broker2"
}
```

- `orders_completed` – number of `Completed order` log lines detected in the
  interval.
- `total_cycles_trillions` – orders multiplied by 0.01.
- `window_start` – timestamp passed to the docker log command.
- `window_end` – newest timestamp observed while parsing logs.
- `service` – docker compose service name used for the scrape.

## Debugging in VS Code

A Go launch configuration is provided in `.vscode/launch.json`. It runs the
scraper with the workspace `.env` file and streams output to the integrated
terminal. Adjust the arguments or environment variables to suit your
deployment.
