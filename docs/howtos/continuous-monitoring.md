# Continuous Monitoring with --follow

Use `--follow` to re-parse log files at a regular interval and refresh
the report automatically.

## Basic usage

```bash
quellog --follow /var/log/postgresql/*.log
```

By default, this analyzes the last 24 hours and refreshes every 30
seconds. The full report is re-printed to the terminal on each cycle.
Press `Ctrl+C` to stop.

## Custom interval and time window

Adjust the refresh interval with `--interval` and the lookback window
with `--last`:

```bash
quellog --follow --interval 1m --last 1h /var/log/postgresql/*.log
```

This refreshes every minute and only considers the last hour of log
entries.

## HTML dashboard

Combine `--follow` with `--html` and `-o` to produce a regularly-rewritten
HTML dashboard (reload the browser to see updates — the page does not
auto-refresh):

```bash
quellog --follow --interval 5m --html \
  -o /tmp/quellog-dashboard.html \
  /var/log/postgresql/*.log
```

Open `/tmp/quellog-dashboard.html` in a browser. quellog rewrites the
file on each refresh cycle — reload the browser to see updates.

## JSON output for external tools

Feed structured data to monitoring pipelines or alerting systems:

```bash
quellog --follow --interval 1m --json \
  -o /tmp/quellog.json \
  /var/log/postgresql/*.log
```

The JSON file is overwritten on each cycle.

## Running in the background

Use a systemd service or cron to keep quellog running unattended.

**systemd** -- create `/etc/systemd/system/quellog-monitor.service`:

```ini
[Unit]
Description=quellog continuous log monitoring
After=postgresql.service

[Service]
Type=simple
ExecStart=/usr/local/bin/quellog --follow --interval 5m \
  --html -o /var/www/html/quellog.html /var/log/postgresql/*.log
Restart=on-failure
User=postgres

[Install]
WantedBy=multi-user.target
```

**cron** -- periodic snapshots without `--follow`:

```bash
0 * * * * /usr/local/bin/quellog --last 1h --html --quiet \
  -o /var/www/html/quellog.html /var/log/postgresql/*.log > /dev/null
```

`--quiet` (`-q`) drops the informational messages written to stderr
(processing summary, format detection, archive entries skipped, follow-mode
notices) and the progress bar, and keeps warnings and errors. The `Report
saved to …` confirmation goes to stdout, hence the redirect: cron then mails
only when something went wrong.
