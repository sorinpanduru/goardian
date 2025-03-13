# Goardian

Goardian is a process supervisor and monitoring tool written in Go. It provides a robust way to manage and monitor multiple processes, with features like automatic restarts, graceful shutdowns, and Prometheus metrics integration.

## Features

- Process supervision with automatic restarts
- Multiple process instances support
- Graceful shutdown handling
- Prometheus metrics for monitoring
- Configurable process environment and working directory
- Support for normal exit codes
- Process runtime tracking
- Memory usage monitoring
- Structured logging with color-coded output
- Subprocess output capture and logging

## Installation

```bash
go install github.com/sorinpanduru/goardian/cmd/goardian@latest
```

## Usage

Create a configuration file (e.g., `config.yaml`):

```yaml
log_format: "console"  # or "json" for JSON format
metrics_addr: ":9090"
shutdown_delay: "10s"

processes:
  - name: "web-server"
    command: "python"
    args: ["app.py"]
    working_dir: "/app"
    environment:
      - "PYTHONPATH=/app"
    start_timeout: "30s"
    stop_timeout: "10s"
    restart_delay: "5s"
    max_restarts: 5
    numprocs: 2
    exit_codes: [0, 1]  # These exit codes will trigger an immediate restart without backoff
```

Run Goardian:

```bash
goardian -config config.yaml
```

## Logging

Goardian provides structured logging with two output formats:

### Console Format
When `log_format: "console"` is set, logs are displayed with:
- Color-coded log levels (green for INFO, red for ERROR, yellow for WARN, gray for DEBUG)
- Timestamp in readable format
- Process name and instance information
- Structured attributes for additional context
- Subprocess output capture with stream type (stdout/stderr)

Example console output:
```
[2024-03-21 10:00:00] INFO: process started
  process=web-server instances=2

[2024-03-21 10:00:01] INFO: process output
  process=web-server stream=stdout instance=0 type=subprocess_output
  Starting server on port 8080
```

### JSON Format
When `log_format: "json"` is set, logs are output in JSON format for machine parsing:
```json
{"time":"2024-03-21T10:00:00Z","level":"info","msg":"process started","process":"web-server","instances":2}
```

## Metrics

Goardian exposes the following Prometheus metrics at the `/metrics` endpoint:

- `goardian_process_restarts_total`: Counter of process restarts
- `goardian_process_uptime_seconds`: Gauge showing process uptime
- `goardian_process_memory_bytes`: Gauge showing process memory usage
- `goardian_process_status`: Gauge showing process status (1=running, 0=stopped)
- `goardian_process_runtime_seconds`: Histogram of process runtime durations

Example Prometheus configuration:

```yaml
scrape_configs:
  - job_name: 'goardian'
    static_configs:
      - targets: ['localhost:9090']
```

## Project Structure

```
goardian/
├── cmd/
│   └── goardian/
│       └── main.go           # Main entry point
├── internal/
│   ├── config/
│   │   └── config.go        # Configuration handling
│   ├── logger/
│   │   └── logger.go        # Structured logging with color support
│   ├── metrics/
│   │   └── metrics.go       # Prometheus metrics
│   └── process/
│       └── process.go       # Process management
├── go.mod
└── README.md
```

## Configuration

### Global Configuration

- `log_format`: Log output format ("json" or "console")
- `metrics_addr`: Address to expose Prometheus metrics
- `shutdown_delay`: How long to wait for processes to shutdown

### Process Configuration

- `name`: Process name (used in logs and metrics)
- `command`: Command to execute
- `args`: Command arguments
- `working_dir`: Working directory for the process
- `environment`: Environment variables
- `start_timeout`: How long to wait for process to start
- `stop_timeout`: How long to wait for process to stop
- `restart_delay`: Delay between restarts
- `max_restarts`: Maximum number of restarts before giving up
- `numprocs`: Number of process instances to run
- `exit_codes`: List of normal exit codes that trigger an immediate restart without backoff

## License

GNU General Public License v3.0 (GPL-3.0) 