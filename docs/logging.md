# Logging System

The mcpproxy-go project includes a comprehensive logging system that follows OS-specific standards for log file storage and provides flexible configuration options.

## Overview

The logging system provides:
- **OS-specific standard directory compliance** for log storage
- **Automatic log rotation** with configurable size, age, and backup limits
- **Multiple output formats** (console, structured, JSON)
- **Configurable log levels** (debug, info, warn, error)
- **Thread-safe concurrent logging**
- **CLI and configuration file control**

## Quick Start

### Basic Usage

```bash
# Enable file logging with default settings
mcpproxy serve --log-to-file

# Set log level and enable file logging
mcpproxy serve --log-level debug --log-to-file

# Use custom log file location
mcpproxy serve --log-file /path/to/custom/mcpproxy.log

# Disable file logging (console only)
mcpproxy serve --log-to-file=false
```

### Configuration File

```json
{
  "logging": {
    "level": "info",
    "enable_file": true,
    "enable_console": true,
    "filename": "mcpproxy.log",
    "max_size": 10,
    "max_backups": 5,
    "max_age": 30,
    "compress": true,
    "json_format": false
  }
}
```

## OS-Specific Log Locations

The logging system automatically selects the appropriate directory based on your operating system:

### macOS
- **Location**: `~/Library/Logs/mcpproxy/`
- **Standard**: macOS File System Programming Guide
- **Example**: `/Users/username/Library/Logs/mcpproxy/mcpproxy.log`

### Windows
- **Location**: `%LOCALAPPDATA%\mcpproxy\logs\`
- **Fallback**: `%USERPROFILE%\AppData\Local\mcpproxy\logs\`
- **Standard**: Windows Application Data Guidelines
- **Example**: `C:\Users\username\AppData\Local\mcpproxy\logs\mcpproxy.log`

### Linux
- **Regular User**: `~/.local/state/mcpproxy/logs/` (XDG Base Directory Specification)
- **Root User**: `/var/log/mcpproxy/`
- **XDG Override**: Uses `$XDG_STATE_HOME/mcpproxy/logs/` if set
- **Example**: `/home/username/.local/state/mcpproxy/logs/mcpproxy.log`

### Fallback
- **Location**: `~/.mcpproxy/logs/`
- **Used when**: OS detection fails or standard directories are inaccessible

## Configuration Options

### Log Levels

| Level | Description | Use Case |
|-------|-------------|----------|
| `debug` | Detailed diagnostic information | Development, troubleshooting |
| `info` | General operational messages | Default production logging |
| `warn` | Warning conditions that should be noted | Potential issues |
| `error` | Error conditions that need attention | Problems requiring action |

### File Rotation Settings

| Setting | Type | Default | Description |
|---------|------|---------|-------------|
| `max_size` | int | 10 | Maximum log file size in MB before rotation |
| `max_backups` | int | 5 | Maximum number of backup files to keep |
| `max_age` | int | 30 | Maximum age of backup files in days |
| `compress` | bool | true | Whether to compress rotated log files |

### Output Formats

#### Console Format (default)
```
2025-06-24 09:31:16 | INFO | server/server.go:176 | Starting mcpproxy | {"version": "v0.1.0"}
```

#### JSON Format
```json
{"level":"info","ts":"2025-06-24T09:31:16.519+03:00","caller":"server/server.go:176","msg":"Starting mcpproxy","version":"v0.1.0"}
```

## CLI Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--log-level` | string | "info" | Set the log level (debug, info, warn, error) |
| `--log-to-file` | bool | true | Enable logging to file in standard OS location |
| `--log-file` | string | "" | Custom log file path (overrides standard location) |

## Configuration File Options

```json
{
  "logging": {
    "level": "info",              // Log level: debug, info, warn, error
    "enable_file": true,          // Enable file logging
    "enable_console": true,       // Enable console logging
    "filename": "mcpproxy.log",   // Log filename (in standard directory)
    "max_size": 10,               // Max file size in MB
    "max_backups": 5,             // Number of backup files to keep
    "max_age": 30,                // Max age of backups in days
    "compress": true,             // Compress rotated files
    "json_format": false          // Use JSON format instead of console format
  }
}
```

## Advanced Usage

### Custom Log Location

```bash
# Use a custom log file path
mcpproxy serve --log-file /var/log/myapp/mcpproxy.log

# Use custom path with configuration
{
  "logging": {
    "filename": "/var/log/myapp/mcpproxy.log"
  }
}
```

### JSON Logging for Production

```json
{
  "logging": {
    "level": "info",
    "enable_file": true,
    "enable_console": false,
    "json_format": true,
    "max_size": 100,
    "max_backups": 10,
    "max_age": 90
  }
}
```

### Development Configuration

```json
{
  "logging": {
    "level": "debug",
    "enable_file": true,
    "enable_console": true,
    "json_format": false,
    "max_size": 1,
    "max_backups": 2,
    "max_age": 1
  }
}
```

## Log Rotation

The logging system uses [lumberjack](https://github.com/natefinch/lumberjack) for automatic log rotation:

- **Size-based rotation**: When a log file exceeds `max_size` MB
- **Age-based cleanup**: Removes files older than `max_age` days
- **Count-based cleanup**: Keeps only `max_backups` backup files
- **Compression**: Optionally compresses rotated files with gzip

### Rotation File Naming

```
mcpproxy.log              # Current log file
mcpproxy.log.2025-06-24   # Rotated by date
mcpproxy.log.2025-06-23.gz # Compressed backup
```

## Monitoring and Debugging

### Finding Your Log Files

Use the built-in command to find log directory:

```bash
# The log directory path is shown in startup logs
mcpproxy serve --log-level debug | grep "Log directory configured"
```

### Common Log Patterns

**Startup Information:**
```
Log directory configured | {"path": "/Users/user/Library/Logs/mcpproxy", "os": "darwin", "standard": "macOS File System Programming Guide"}
Starting mcpproxy | {"version": "v0.1.0", "log_level": "info"}
```

**Server Operations:**
```
Server is ready | {"phase": "Ready", "message": "Server is ready"}
Tool called | {"tool": "example:search", "server": "example", "duration": "150ms"}
```

**Error Handling:**
```
Failed to connect to upstream server | {"server": "example", "error": "connection refused"}
```

## Performance Considerations

### Log Level Impact

| Level | Performance | Use Case |
|-------|-------------|----------|
| `error` | Minimal overhead | Production (errors only) |
| `warn` | Low overhead | Production (recommended) |
| `info` | Moderate overhead | Production (verbose) |
| `debug` | High overhead | Development only |

### File vs Console Logging

- **File logging**: Better for production, log rotation, persistence
- **Console logging**: Better for development, real-time monitoring
- **Both enabled**: Flexible but higher overhead

## Troubleshooting

### Log Directory Not Created

1. **Check permissions**: Ensure write access to the standard directory
2. **Check disk space**: Ensure sufficient space for log files
3. **Use custom path**: Override with `--log-file` if needed

```bash
# Test with custom path
mcpproxy serve --log-file ./mcpproxy.log --log-level debug
```

### Log Rotation Not Working

1. **Check file size**: Ensure logs exceed `max_size` threshold
2. **Check permissions**: Ensure write access for rotation
3. **Check configuration**: Verify rotation settings

### Performance Issues

1. **Reduce log level**: Use `warn` or `error` in production
2. **Disable console logging**: Use file-only logging
3. **Increase rotation size**: Reduce rotation frequency

```json
{
  "logging": {
    "level": "warn",
    "enable_console": false,
    "max_size": 100
  }
}
```

## Testing

The logging system includes comprehensive tests:

```bash
# Run all logging tests
go test ./internal/logs

# Run E2E logging tests
go test ./internal/logs -run TestE2E

# Test specific functionality
go test ./internal/logs -run TestE2E_LogRotation
go test ./internal/logs -run TestE2E_ConcurrentLogging
```

## Security Considerations

- **Log file permissions**: Files created with 0644 permissions
- **Directory permissions**: Directories created with 0755 permissions
- **Sensitive data**: Avoid logging sensitive information
- **Log rotation**: Old logs are automatically cleaned up

## Integration Examples

### Docker Deployment

```dockerfile
# Mount log directory as volume
VOLUME ["/var/log/mcpproxy"]

# Configure for container logging
ENV LOG_LEVEL=info
ENV LOG_TO_FILE=true
```

### Systemd Service

```ini
[Unit]
Description=MCP Proxy Service

[Service]
ExecStart=/usr/local/bin/mcpproxy serve --log-to-file --log-level info
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

### Log Aggregation

For centralized logging with tools like ELK stack or Fluentd:

```json
{
  "logging": {
    "json_format": true,
    "enable_console": false,
    "max_size": 50,
    "compress": false
  }
}
```

## API Reference

### Log Directory Functions

```go
// Get OS-specific log directory
logDir, err := logs.GetLogDir()

// Get log directory information
info, err := logs.GetLogDirInfo()

// Ensure log directory exists
err := logs.EnsureLogDir(logDir)

// Get full path for log file
logPath, err := logs.GetLogFilePath("mcpproxy.log")
```

### Logger Setup

```go
// Setup logger with configuration
config := &config.LogConfig{
    Level:         "info",
    EnableFile:    true,
    EnableConsole: true,
    // ... other options
}
logger, err := logs.SetupLogger(config)
```

## Standards Compliance

The logging system follows established standards for each operating system:

- **macOS**: [File System Programming Guide](https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/FileSystemProgrammingGuide/)
- **Windows**: [Application Data Guidelines](https://docs.microsoft.com/en-us/windows/win32/shell/knownfolderid)
- **Linux**: [XDG Base Directory Specification](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html)

This ensures that logs are stored in the expected locations for each platform, making them easy to find and manage. 