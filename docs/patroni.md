# Patroni Reports

quellog recognizes [Patroni](https://patroni.readthedocs.io/) logs by their content and reports on the cluster they describe: who held the leader lock and since when, every leader change with its cause and the time spent without a leader, DCS incidents, and failovers refused because of replication lag.

```bash
quellog /var/log/patroni/patroni-*.log                    # one file per member
quellog /var/log/patroni/patroni-*.log --patroni-detail   # the detailed view
quellog --html node1/patroni.log node2/patroni.log node3/patroni.log
```

Give one Patroni log per member: most Patroni messages do not name the member that wrote them, so quellog reads each file as one member's point of view and merges them. The member name comes from the log itself (`I am (pg1)`). A member named in the given logs (as leader, or as a peer another member contacted) but whose own log was not given is reported under **No log from**: what involves it, its lock terms, the time without leader around them, its DCS losses, is then seen by the other members only, and a leader at the end is marked *as seen by the others*.

!!! tip "Try it now"
    **[Open the demo on a Patroni lab run →](https://alain-l.github.io/quellog/demo.html?demo=patroni)**: three members' Patroni and PostgreSQL logs, analyzed in your browser.

## Combining with PostgreSQL logs

Patroni and PostgreSQL logs can be passed together. The PostgreSQL report is then followed by the Patroni section (`patroni` key in JSON and YAML), and each leader change lists the PostgreSQL lifecycle events (shutdown, start, crash recovery, reload) logged from just before the old leader was last seen to 30 seconds after the change.

```bash
quellog node*/patroni.log node*/postgresql-*.log
```

The events come from all the PostgreSQL logs given, without telling the nodes apart: the same event logged by two nodes reads `×2`. The PostgreSQL report itself merges the nodes' logs as quellog always does for several files, so its SERVER timeline lists each file's events in turn.

The two logs are matched on the wall clock: Patroni writes its timestamps without a time zone, so Patroni and PostgreSQL are assumed to log in the same zone, which is the default on a node.

## What is parsed

- The default text format (`2026-09-24 13:44:33,372 INFO: ...`), plain or gzip/zstd compressed.
- The same lines behind a `journalctl` prefix (`Sep 24 13:44:33 db1 patroni[812]: 2026-09-24 ...`).
- The JSON format (`log: type: json`), with Patroni's default `asctime`, `levelname` and `message` fields.

## Report sections

The text and Markdown reports are synthetic by default: the period, the members, the leader at the end, the leader changes, the time without leader, the DCS incidents, the counts of errors and warnings, and the five most frequent errors. `--patroni-detail` gives the detailed report alone, every section below; `--full` includes it in the full report. JSON and YAML always hold every detail; the HTML report shows it without the message IDs, which serve `--event-detail` in the CLI.

- **Leader at end**: the member holding the leader lock when the logs end, and since when; `none` if the last word is that nobody holds it.
- **Leader changes**: the lock moving to another member. A change is a **switchover** when it was asked for (a switchover request or a graceful demote shortly before, or the new leader clearing the switchover key), otherwise a **failover**. A member taking its own lock back after losing it (typically once the DCS is back) is listed as **retaken**, and not counted as a change.
- **Time without leader**: for each change, from the last time the old leader said it held the lock to the first sighting of the new one. The total is the sum over the listed changes, retaken ones included.
- **DCS incidents**: spans during which at least one member reported *DCS is not accessible*, from that verdict to the member's next report of its role: the time Patroni took to notice is not included, so an incident reads shorter than the outage itself. Losses that overlap in time are one incident. Connection retries that Patroni recovers from without that verdict are not incidents; they appear under warnings and errors.
- **Refused for lag**: *My wal position exceeds maximum replication lag*, logged by a replica that declines to become leader. Patroni does not log the lag itself.
- **Timeline**: bootstrap, replicas created, switchover requests, demotions, promotions, pg_rewind (and its failures), reinit, crash recovery, pause and resume, DCS lost and back.
- **Warnings and errors**: grouped by message, values masked, laid out as the EVENTS section: per level, each message with its ID, count, share and first and last occurrence. `--event-detail` (`-E`) takes the ID, as for a PostgreSQL event: the occurrences over time, the members whose log wrote it, the first occurrence as written.

## JSON keys

`start_date`, `end_date`, `duration`, `total_logs`, `error_count`, `warning_count`, `leader`, `leader_since`, `leader_changes`, `switchovers`, `failovers`, `lock_retaken`, `leaderless_time`, `dcs_incidents`, `members`, `members_without_log`, `leadership` (lock terms), `changes`, `event_counts`, `events`, `messages` (each with its `id`, `first_date` and `last_date`). Timestamps and durations use the same formats as the rest of the report.

## Limitations

- A Patroni log inside a tar, zip or 7z archive is skipped with a warning: extract it first.
- Standard input is always read as a PostgreSQL log, so a Patroni log must be given as a file.
- A file is read either as a Patroni log or as a PostgreSQL log, whichever writes most of its lines; the other's lines are skipped, with a warning when the file is read as Patroni's.
- The section flags (`--summary`, `--events`, ...) select PostgreSQL sections: the Patroni section is part of the full report, or is the whole report when no PostgreSQL log is given.
- Only the time filters (`--begin`, `--end`, `--last`, `--window`) apply; the database, user and application filters select PostgreSQL sessions. In the HTML report the Patroni section is not re-scoped by the time slider.
- `--split` is rejected when a Patroni log is given.
- In the browser, drop or select the members' logs together, each as its own file: the members of an archive are joined into one stream there.
