# Incident Triage

When investigating an incident, start with a high-level overview then drill down.

## Step 1: High-level overview

```bash
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --summary --events
```

## Step 2: If errors found, check SQL and locks

```bash
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --sql-performance --locks --tempfiles
```

## Step 3: Deep dive into specific queries

```bash
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --sql-detail se-a1b2c3
```

!!! note "Work in progress"
    This how-to will be expanded with real-world examples and interpretation guidance.
