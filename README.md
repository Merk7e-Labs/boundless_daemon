# Boundless Scraper

Go-based scraper that reads docker compose logs for the configured broker
service, counts `Completed order` entries since the last processed timestamp,
calculates total cycles (0.01 trillion cycles per order), and POSTs the metrics
to a remote endpoint. A JSON state file lets the scraper resume after restarts
without double counting.

## Requirements

- Go 1.22+
- Docker CLI available on the host (for running the configured log command)

## Project layout

```
.
├── cmd/
│   ├── mockserver/             # Local HTTP server to inspect payloads
│   └── scraper/                # Production entrypoint (flags, signal handling)
├── internal/
│   ├── app/                    # Scheduler loop & orchestration
│   ├── config/                 # Runtime configuration loader/validation
│   ├── envfile/                # dotenv loader used by the CLI
│   ├── filesystem/             # Path helpers (tilde expansion, abs paths)
│   ├── scraper/                # Log parsing, docker invocation, HTTP posting
│   └── state/                  # Persistent timestamp storage helpers
├── .env.example                # Sample configuration
├── agg_state.json              # Example state snapshot
└── README.md
```

## Configuration

 Edit `.env.example` (the default env file) and adjust the values as needed,
 or pass `--env /path/to/file` to load a different dotenv file.

| Variable | Description | Default |
| --- | --- | --- |
| `SCRAPER_ENDPOINT` | **Required.** HTTP endpoint that receives the JSON payload. | _(none)_ |
| `SCRAPER_SERVICE` | Docker compose service name to read logs from. | `broker` |
| `SCRAPER_LOG_COMMAND` | Command template used to fetch logs. `{service}` and `{since}` placeholders are supported. | `docker compose logs {service} --since {since} --no-color` |
| `SCRAPER_WORKDIR` | Directory from which the log command is executed. | `~/boundless` |
| `SCRAPER_LOG_FILE` | Optional log file to read instead of executing a command. | _(empty)_ |
| `SCRAPER_STATE_FILE` | Path to the JSON file storing scraper state. | `scraper_state.json` |
| `SCRAPER_INTERVAL` | Interval between scrapes (Go duration, e.g. `60s`, `5m`). | `15m` |
| `SCRAPER_COMMAND_TIMEOUT` | Timeout for the docker log command. | `60s` |
| `SCRAPER_POST_TIMEOUT` | Timeout for posting metrics. | `15s` |
| `SCRAPER_PROVER_ID` | Optional prover identifier reported with metrics. | _(empty)_ |
| `SCRAPER_PROVER_ADDRESS` | Optional prover address sent with payloads. | _(empty)_ |

You can also point `--env` at any dotenv file (default `.env`). Additional env
files can be chained with `SCRAPER_ENV_FILE` and `SCRAPER_BROKER_ENV_FILE`.

## Running locally

Run a single scrape for debugging:

```bash
go run ./cmd/scraper -once
```

Run continuously (default behaviour):

```bash
go run ./cmd/scraper
```

### Boundless dashboard / port forwarding workflow

If you rely on SSH reverse tunnels to feed the Boundless dashboard, keep the
existing steps in place:

```bash
ssh -p15306 -R 19090:127.0.0.1:9090 user01@120.240.236.185
ssh -p15306 -R 29090:127.0.0.1:9090 user01@120.240.236.185

export SCRAPER_LOG_COMMAND="docker compose logs {service} {since} --no-color"
export SCRAPER_ENDPOINT="http://127.0.0.1:19090/api/offchain/report"
export SCRAPER_ENDPOINT="http://127.0.0.1:29090/api/offchain/report"
```

Set `SCRAPER_WORKDIR` (for example `~/boundless`) to the directory that contains
`docker-compose.yml`, match `SCRAPER_SERVICE` with the running docker compose
service (`broker`, `broker2`, `broker3`, ...), and export
`SCRAPER_PROVER_ID=<your prover id>` and
`SCRAPER_PROVER_ADDRESS=<your prover address>`. You can place these exports directly in
`.env` so the scraper always uses the forwarded dashboard endpoints.

## Building

Standard build on the host OS:

```bash
go build -o dist/boundless-scraper ./cmd/scraper
```

Cross-compile a Linux AMD64 binary from Windows PowerShell:

```powershell
$Env:GOOS = "linux"
$Env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w" -o dist/boundless-scraper ./cmd/scraper
Remove-Item Env:GOOS, Env:GOARCH
```

## Deploying to Ubuntu with systemd

1. Copy the binary plus your `.env` files to the server (example using `scp`):
   ```powershell
   scp dist/boundless-scraper ubuntu@your-server:/opt/boundless-scraper/bin/
   scp .env ubuntu@your-server:/opt/boundless-scraper/config/.env
   scp .env.broker ubuntu@your-server:/opt/boundless-scraper/config/.env.broker
   ```
   Ensure `/opt/boundless-scraper` is owned by the service user
   (`sudo chown -R ubuntu:ubuntu /opt/boundless-scraper`).

2. Create `/etc/systemd/system/boundless-scraper.service`:
   ```
   [Unit]
   Description=Boundless scraper
   Wants=network-online.target
   After=network-online.target

   [Service]
   Type=simple
   User=ubuntu
   Group=ubuntu
   WorkingDirectory=/opt/boundless-scraper
   EnvironmentFile=/opt/boundless-scraper/config/.env
   EnvironmentFile=/opt/boundless-scraper/config/.env.broker
   ExecStart=/opt/boundless-scraper/bin/boundless-scraper --env /opt/boundless-scraper/config/.env
   Restart=on-failure
   RestartSec=5s

   [Install]
   WantedBy=multi-user.target
   ```

3. Enable and start the service:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now boundless-scraper.service
   sudo systemctl status boundless-scraper.service
   ```

4. Tail logs via journald:
   ```bash
   journalctl -u boundless-scraper.service -f
   ```

For updates, copy the new binary, run `sudo systemctl restart boundless-scraper`,
and confirm the new version via `journalctl`.
