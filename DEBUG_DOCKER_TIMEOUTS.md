# Docker Timeout Debugging Guide

## What You've Implemented ✅

Your refactoring has added comprehensive monitoring for Docker containers:

### 1. **Stdin/Stdout Pipe Monitoring**
- **Pipe Status Detection**: Detects when stdin/stdout pipes are broken or closed
- **Container Death Detection**: Logs when the container process likely died
- **Graceful Closure Detection**: Distinguishes between graceful shutdown and crashes

### 2. **Error Source Tracking** 
- **Context Timeout vs Container Death**: Enhanced error messages distinguish between:
  - `context deadline exceeded` (timeout triggered by CLI tool)
  - `broken pipe` / `closed pipe` (container died or crashed)
  - Container unresponsive vs container dead

### 3. **Real-time Health Monitoring**
- **Stderr Stream Monitoring**: All stderr output logged to both main and server-specific logs
- **Docker Daemon Connectivity**: Periodic checks to verify Docker daemon is reachable
- **Process Health Checks**: Built-in health check method for connection validation

### 4. **Enhanced Timeout Diagnostics**
- **Early Warning System**: Health checks triggered during long operations
- **Post-timeout Analysis**: Additional diagnostics run after timeouts
- **Comprehensive Diagnostics**: Full connection state reporting

## Debugging Your Timeout Issue

Based on your logs, here's what's happening:

### Timeline Analysis:
1. **14:55:03**: Docker container starts successfully (initialization works)
2. **14:55:03**: ListTools request sent 
3. **14:55:13**: Timeout after exactly 10 seconds (`--timeout=10s`)
4. **Root Cause**: Container became unresponsive after startup, not dead

### Debug Commands to Use:

```bash
# 1. Test with longer timeout to see if container is just slow
mcpproxy tools list --server=fetch-reference-wrapper-2 --log-level=debug --timeout=60s

# 2. Monitor in real-time with tail command for detailed logs
tail -f ~/Library/Logs/mcpproxy/main.log | grep -E "(pipe|timeout|health|stderr|docker)"

# 3. Test Docker container manually to see startup time
docker run -i --rm ghcr.io/github/github-mcp-server

# 4. Check Docker daemon status
docker version
docker system info
```

### Enhanced Logging Available:

The new implementation provides these log messages:

**Connection Health:**
- `⏰ ListTools taking longer than expected - checking connection health...`
- `Connection health check failed`
- `❌ Failed to list tools: TIMEOUT after 30 seconds`
- `❌ Failed to list tools: PIPE BROKEN`

**Pipe Monitoring:**
- `Stdin/stdout pipe closed - container may have died`
- `Stderr stream ended`
- `Docker connectivity check failed`

**Process Monitoring:**
- `Started process monitoring`
- `Docker daemon appears to be unreachable`

## Recommendations

### 1. **Immediate Testing**
```bash
# Test with longer timeout first
mcpproxy tools list --server=fetch-reference-wrapper-2 --log-level=debug --timeout=60s

# Monitor logs in separate terminal
tail -f ~/Library/Logs/mcpproxy/main.log | grep -E "(health|timeout|pipe|docker)"
```

### 2. **Diagnose Container Behavior**
```bash
# Test the exact Docker command manually
docker run -i --rm ghcr.io/github/github-mcp-server

# Check if container starts but gets stuck
docker run -i --rm ghcr.io/github/github-mcp-server &
docker ps  # Should show running container
```

### 3. **Enable Comprehensive Debugging**
The new implementation automatically provides:
- Health checks during long operations
- Pipe monitoring for container death detection
- Enhanced error messages with diagnostics
- Docker daemon connectivity verification

### 4. **Look for These New Log Messages**
When you run the command again, watch for:
- Early health check warnings after 5 seconds
- Pipe status messages if container dies
- Enhanced timeout diagnostics
- Docker daemon connectivity status

## Next Steps

1. **Test with longer timeout**: See if container just needs more startup time
2. **Monitor the enhanced logs**: The new implementation will tell you exactly what's failing
3. **Test container manually**: Verify the Docker image works outside mcpproxy
4. **Check Docker daemon**: Ensure Docker itself is working properly

The enhanced implementation now provides the comprehensive monitoring you requested! 🎉