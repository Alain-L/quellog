# Continuous Monitoring with --follow

Monitor log files in real-time with periodic refresh.

## Basic Usage

```bash
# Refresh every 30 seconds (default), last 24 hours
quellog --follow /var/log/postgresql/*.log

# Custom interval
quellog --follow --interval 1m /var/log/postgresql/*.log

# Shorter time window
quellog --follow --last 1h /var/log/postgresql/*.log
```

Press `Ctrl+C` to stop.

## JSON Output for External Tools

```bash
# Write to file for Grafana or other monitoring tools
quellog --follow --json --output /tmp/quellog.json /var/log/postgresql/*.log
```

## HTML Dashboard

```bash
# Auto-refreshing HTML report
quellog --follow --interval 5m --html /var/log/postgresql/*.log
```

!!! note "Work in progress"
    This how-to will be expanded with systemd service setup and integration examples.
