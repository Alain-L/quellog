# Quellog

Quellog is a fast, reliable, and easy-to-use CLI tool for parsing and filtering PostgreSQL logs. It provides summary reports, detailed SQL performance analysis, and various filtering options to help you quickly gain insights from your logs.

## Features

- **Time-based Filtering:** Filter log entries by start/end dates or a custom time window.
- **Attribute Filters:** Filter by database, user, application, or other attributes.
- **SQL Performance Reporting:** 
  - Generate a global SQL summary including performance metrics and percentiles.
  - View details for specific SQL query IDs.
  - See top lists for slowest, most frequent, and most time-consuming queries.
- **Event and Maintenance Reporting:** 
  - Automatic detection of events (errors, warnings, etc.)
  - Maintenance metrics for vacuum, analyze, and checkpoint operations.
- **Customizable Output:** Supports both summary and detailed line-by-line output.
- **Extensible:** Designed to be further extended with a TUI interface or additional API support.

## Installation

Clone the repository and build the binary:

```sh
git clone https://github.com/yourusername/quellog.git
cd quellog
go build -o bin/quellog
```

## Usage

Basic usage example:

```sh
./bin/quellog /path/to/logs --summary
```

For more details, run:

```sh
./bin/quellog --help
```

## Contributing

Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details on our code of conduct, and the process for submitting pull requests.

## License

This project is licensed under the terms of the MIT License. See [LICENSE.md](LICENSE.md) for details.