---
name: fleet-health-sweep
description: Triage many SQLite databases at once — per-tenant, per-edge or per-agent files — worst-first, and find the ones that are corrupt, bloated, drifted or have stopped being written to. Use when the user has a fleet, a database-per-tenant setup, or asks about the health of many databases.
when_to_use: Triggered by "check all tenant databases", "database per tenant", "fleet health", "which databases are unhealthy", "did any database stop updating", "schema drift across tenants".
---

# Fleet sweep

Generic monitoring watches servers. A SQLite fleet is a pile of *files* spread
across tenants, edge locations and devices, which is exactly what those tools
cannot see. The questions worth asking are fleet-shaped.

## Set up the fleet config once

`litescope.fleet.yaml`:

```yaml
databases:
  - name: acme
    dsn: /srv/tenants/acme.db
    tags: [prod, us]
  - name: globex
    dsn: d1://<database-id>
    tags: [prod, eu]
```

`dsn` accepts anything litescope understands: a path, `d1://`, `turso://`.

## The four sweeps

### Which databases are unhealthy?

```
litescope_fleet_health(config="litescope.fleet.yaml")
```

Returns every database worst-first with its severity and issues. Add
`deep=true` for a full `integrity_check` — much slower, so use it on a
suspicion, not on a schedule.

### Which ones stopped being written to?

A tenant database that has had no write in 24h is either idle or broken, and
only the owner knows which — but nothing else will tell you it happened:

```
litescope fleet health --stale-after 24h
```

### Which ones drifted from the baseline schema?

```
litescope_fingerprint(source=...)     # one database's schema fingerprint
litescope fleet check                 # drift across the whole fleet
litescope fleet fingerprint           # how many distinct schemas you actually run
```

Drift is the signal that a migration half-applied: some tenants got it, some
didn't.

### Where is the lock contention?

```
litescope fleet locks
```

Rolls every database's recorded contention into one worst-first view — locked
windows, wait p95, top holders, WAL checkpoint starvation. It exits non-zero
when anything is critical, so it works as a cron check.

## How to report a sweep

Lead with the count that matters, not the list: "3 of 412 critical, all in the
EU region, all the same WAL bloat." Then the three, with their issue. A wall of
412 rows tells the user nothing they can act on.

## Rules that matter

- **Filter with tags** (`tag="prod"`) rather than sweeping everything when the
  fleet is large; a deep sweep reads every page of every file.
- **A fleet operation is still N separate databases.** A fix that works on one
  is a hypothesis about the rest — verify per database rather than assuming.
- Anything that writes across a fleet (migrations, optimisation) should go
  through a canary cohort first, and every tenant gets its own snapshot before
  it is touched.
