# TODO

## General Enhancements

- **Events Report:**  
  - Check for parameter changes in logs (e.g., ALTER SYSTEM, configuration reload, etc.) and include these in the "events" report.
- **Query Hashing:**  
  - Implement a robust hash mechanism for queries.
  - Use the PostgreSQL query ID if available.
- **Security Tab:**  
  - Detect password changes.
  - Alert when plain-text passwords are detected.
- **Benchmark Mode:**  
  - Implement a benchmark mode (e.g., using a `--benchmark` flag) to measure performance.

## Data Structures

- **LogEntry:**  
  - Review and possibly refine the LogEntry structure.
- **Query/SQLStatement:**  
  - Define a clear structure to represent individual SQL queries/statements.

## Terminal User Interface (TUI)

- Evaluate and possibly integrate libraries such as:
  - [tview](https://github.com/rivo/tview)
  - [Bubble Tea](https://github.com/charmbracelet/bubbletea)
- autocompletion

## Reporting Improvements

- **Query ID:**  
  - Use the PostgreSQL-provided query ID if it exists.
- **Top Queries Reporting:**  
  - Instead of a fixed TOP 10, consider reporting the top 80/90/99 percentiles.
  - Do not display all columns in every top list.
  - Different categories:
    - Slowest individual queries
    - Slowest normalized queries (taking case sensitivity into account)
    - Slowest individual queries with parameters
  - Create a synthetic header that summarizes total queries, total SELECT, total INSERT, total UPDATE, etc.
  - Verify labels and text for consistency.
  - Implement a `--sql-detail` report to show detailed information for a given query ID.

## API Considerations

- Evaluate API options for internal data access:
  - REST
  - gRPC with Protocol Buffers

## SQL-Specific Analysis

- **Busiest Windows:**  
  - Identify the window(s) with the highest query time (peak windows).
- **Similar Queries:**  
  - Group similar queries together.
- **Time-Consuming Queries:**  
  - Currently, time-consuming queries are based on individual metrics (similar to pgBadger); consider using normalized queries for this metric.
- **Duration Histogram:**  
  - Implement a histogram for query durations.


## Other
- add test suite
- add nice doc
- package debian
- docker
- security
- flags validation
- **verbose mode**
