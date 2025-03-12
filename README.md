# Goardian

A process supervisor written in Go that manages and monitors subprocesses with configurable restart strategies and output handling.

## Features

- YAML-based configuration
- Process monitoring and automatic restart
- Configurable backoff strategy for restarts
- Maximum restart limit
- Structured logging with color-coded output
- Subprocess output capture and logging
- Graceful shutdown handling

## Installation

```bash
go install github.com/sorinpanduru/goardian@latest
```

## Usage

```bash
goardian -config config.yaml
```

## Configuration

The configuration file is in YAML format. Here's an example with all available options:

```yaml
processes:
  - name: "web-server"
    command: "/usr/local/bin/nginx"
    args: ["-c", "/etc/nginx/nginx.conf"]
    start_timeout: "5s"
    restart_delay: "5s"
    max_restarts: 5
    working_dir: "/var/www"
    environment:
      - "ENV=production"
      - "PORT=8080"
    backoff_enabled: true
    backoff_factor: 2.0
    backoff_max_delay: "1m"

  - name: "api-service"
    command: "/usr/local/bin/api-server"
    args: ["--port", "3000"]
    start_timeout: "3s"
    restart_delay: "3s"
    max_restarts: 3
    working_dir: "/opt/api"
    environment:
      - "NODE_ENV=production"
      - "DB_HOST=localhost"
    backoff_enabled: true
    backoff_factor: 1.5
    backoff_max_delay: "30s"
```

### Configuration Options

- `name`: Unique identifier for the process
- `command`: Path to the executable
- `args`: Command line arguments
- `start_timeout`: How long to wait for process to start
- `restart_delay`: Initial delay between restart attempts
- `max_restarts`: Maximum number of restart attempts
- `working_dir`: Working directory for the process
- `environment`: List of environment variables
- `backoff_enabled`: Enable exponential backoff for restarts
- `backoff_factor`: Multiplier for the backoff delay
- `backoff_max_delay`: Maximum delay between restarts

## Output Examples

### Process Management Logs

```
2024-03-21 14:30:45.123 level=INFO msg="started process" process=web-server
2024-03-21 14:30:45.234 level=INFO msg="started process" process=api-service
2024-03-21 14:30:46.345 level=ERROR msg="failed to start process" process=web-server error="command not found"
2024-03-21 14:30:46.456 level=INFO msg="waiting before next restart attempt" process=web-server delay=5s
2024-03-21 14:30:51.567 level=INFO msg="successfully restarted process" process=web-server
2024-03-21 14:30:52.678 level=INFO msg="process reached maximum restart limit" process=api-service max_restarts=3
2024-03-21 14:30:53.789 level=INFO msg="received shutdown signal, initiating graceful shutdown"
2024-03-21 14:30:53.890 level=INFO msg="stopping all processes"
2024-03-21 14:30:53.901 level=INFO msg="shutdown complete"
```

### Subprocess Output Logs

```
2024-03-21 14:30:45.123 level=INFO msg="nginx: the configuration file /etc/nginx/nginx.conf syntax is ok" process=web-server stream=stdout type=subprocess_output
2024-03-21 14:30:45.234 level=INFO msg="nginx: configuration file /etc/nginx/nginx.conf test is successful" process=web-server stream=stdout type=subprocess_output
2024-03-21 14:30:45.345 level=INFO msg="Starting API server on port 3000" process=api-service stream=stdout type=subprocess_output
2024-03-21 14:30:45.456 level=INFO msg="Connected to database at localhost" process=api-service stream=stdout type=subprocess_output
2024-03-21 14:30:46.567 level=INFO msg="Error: Failed to connect to database" process=api-service stream=stderr type=subprocess_output
```

## Log Format

Each log line follows this format:
```
timestamp level=LEVEL msg="message" [attributes...]
```

### Log Levels
- `INFO`: Green
- `WARN`: Yellow
- `ERROR`: Red
- `DEBUG`: Default color

### Common Attributes
- `process`: Name of the process
- `stream`: For subprocess output, either "stdout" or "stderr"
- `type`: For subprocess output, set to "subprocess_output"
- `error`: Error message when applicable
- `delay`: Delay duration for restart attempts
- `max_restarts`: Maximum number of restart attempts

## License

GNU General Public License v3.0 