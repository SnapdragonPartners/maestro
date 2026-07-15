+++
title = "Metrics System Demo"
edit_date = "2026-07-15"
status = "archive"
+++

# Metrics System Demo

This document shows how to verify that the metrics system is working correctly.

## Quick Tests

### 1. Factory Metrics Configuration
Run the metrics configuration tests to see logging in action:

```bash
# Test different metrics configurations
go test -v ./pkg/agent -run TestMetrics
```

Expected output shows:
- ✅ **Prometheus enabled**: `📊 Initializing Prometheus metrics recorder (enabled=true, exporter=prometheus, url=http://localhost:9090)`
- ✅ **Metrics disabled**: `📊 Using no-op metrics recorder (enabled=false, exporter=prometheus)`
- ✅ **Noop exporter**: `📊 Using no-op metrics recorder (enabled=true, exporter=noop)`

### 2. Persistence Layer Configuration
Run the persistence metrics tests to see config parsing:

```bash
# Test metrics configuration parsing
go test -v ./pkg/persistence -run TestMetrics
```

Expected output shows:
- ✅ **Fully configured**: `📊 Checking metrics config: enabled=true, exporter=prometheus, url=http://localhost:9090`
- ✅ **Disabled metrics**: `📊 Checking metrics config: enabled=false, exporter=prometheus, url=http://localhost:9090`
- ✅ **Nil config**: `📊 Checking metrics config: enabled=false, exporter=nil, url=nil`

### 3. Build Verification
Verify the system builds correctly with metrics enabled:

```bash
make build
```

## Current Defaults

The system now uses **metrics-enabled** defaults:

```json
{
  "agents": {
    "metrics": {
      "enabled": true,                    // ✅ Enabled by default
      "exporter": "prometheus",           // ✅ Uses Prometheus by default
      "namespace": "maestro",
      "prometheus_url": "http://localhost:9090"
    }
  }
}
```

## What This Means

### ✅ **With Default Config** (no config file):
- Metrics will be **collected** via Prometheus recorder
- Token counts and costs will be **persisted** to database
- Users will see real metrics data in story completion logs

### ⚠️ **Without Prometheus Server**:
- Metrics collection still works (via recorder)
- Database persistence of individual LLM calls works
- Story completion queries to Prometheus will fail (logged as warnings)
- Final story metrics in database may be incomplete

### 🔧 **To Disable Metrics**:
Users can create a config file with:
```json
{
  "agents": {
    "metrics": {
      "enabled": false
    }
  }
}
```

## Production Usage

For full metrics functionality:
1. Run Prometheus server on `localhost:9090` (or configure different URL)
2. Maestro will automatically collect and persist metrics
3. Story completion will include token counts and costs in database

The logging helps users understand exactly what's happening with their metrics configuration.