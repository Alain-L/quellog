# quellog Documentation

This directory contains the documentation for quellog, built using [MkDocs](https://www.mkdocs.org/) with the [Material theme](https://squidfunk.github.io/mkdocs-material/).

## Building the Documentation

### Prerequisites

Install MkDocs and the Material theme:

```bash
pip install mkdocs mkdocs-material
```

### Local Preview

Serve the documentation locally with auto-reload:

```bash
mkdocs serve
```

Then open http://127.0.0.1:8000 in your browser.

### Building Static Site

Generate the static HTML site:

```bash
mkdocs build
```

The output will be in the `site/` directory.

## Documentation Structure

- `index.md` - Home page with overview, features, and quick start guide
- `installation.md` - Detailed installation instructions
- `postgresql-setup.md` - PostgreSQL configuration guide
- `formats.md` - Supported log formats documentation
- `filtering-logs.md` - Log filtering options
- `filtering-output.md` - Output section filtering
- `default-report.md` - Default report sections explained
- `sql-reports.md` - SQL analysis deep dive
- `json-export.md` - JSON export format and usage
- `markdown-export.md` - Markdown export format and usage

## Contributing

When adding or updating documentation:

1. Edit the relevant `.md` files in `docs/`
2. Preview changes with `mkdocs serve`
3. Ensure links work correctly
4. Check for typos and clarity
5. Update `mkdocs.yml` navigation if adding new pages

## Deployment

The documentation can be deployed to GitHub Pages:

```bash
mkdocs gh-deploy
```

This builds the site and pushes it to the `gh-pages` branch.
