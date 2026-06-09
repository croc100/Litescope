# Litescope

> Human-readable diff for SQLite databases.

`sqldiff` dumps SQL. Litescope shows you what actually changed.

## Usage

```bash
# Compare two databases
litescope diff old.db new.db

# HTML report
litescope diff old.db new.db --html report.html

# Inspect schema of a single file
litescope schema app.db
```

## Output

```
Schema diff
  ~ users        column added: verified_at (TEXT)
  + audit_logs   new table (3 columns)
  - sessions     table removed

Data diff
  users          +12 rows  -3 rows  ~5 rows
  audit_logs     +248 rows
```

## Install

```bash
pip install litescope
```

Or download a binary from [Releases](https://github.com/croc100/Litescope/releases).

## Roadmap

- [x] Schema diff
- [x] Data diff (row counts + changed row samples)
- [x] `--html` report
- [ ] Watch mode (live diff)
- [ ] 3-way diff
- [ ] Migration SQL generation
- [ ] GUI

## License

[Elastic License 2.0](LICENSE) — free for individuals and internal use. Contact for commercial distribution or SaaS use.
