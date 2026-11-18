# F-journalctl

`pg1.patroni.log` is run A's `pg1.patroni.log` passed through journald on dalibo-pc
(`systemd-cat -t <tag> < pg1.patroni.log`) and read back with
`journalctl --user -t <tag> -o short`, the way a DBA exports a Patroni running under systemd.
The journal prefix carries the export time; the Patroni timestamps inside are the original ones.
Ground truth: identical to run A's pg1 (the report on this file alone must equal the report on
`../A-p4-text-async/pg1.patroni.log` alone).
