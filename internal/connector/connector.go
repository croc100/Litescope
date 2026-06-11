// Package connector provides a unified interface for loading SQLite schemas
// from local files or remote sources (Turso, Cloudflare D1).
package connector

import (
	"fmt"
	"strings"

	"github.com/croc100/litescope/internal/schema"
)

// Connector loads a schema from any SQLite source.
type Connector interface {
	Schema() (*schema.Schema, error)
	Close() error
	// DSN returns the original connection string (for display).
	DSN() string
}

// Executor is implemented by connectors that can run migration statements.
//
// Exec runs the given statements as a unit. Implementations execute inside a
// transaction where the underlying provider supports it (local files, Turso),
// rolling back on any failure; providers without interactive transactions over
// their API (Cloudflare D1) run statements sequentially and stop on the first
// error. Capabilities() describes the guarantees a given connector provides.
type Executor interface {
	// Exec runs the statements. When dryRun is true, the executor applies them
	// inside a transaction and rolls back instead of committing — validating the
	// migration without changing data. Providers without transactions (D1) return
	// an error for dryRun.
	Exec(statements []string, dryRun bool) error
	Capabilities() ExecCapabilities
}

// ExecCapabilities describes the safety guarantees of an Executor.
type ExecCapabilities struct {
	// Transactional is true when a failed statement rolls back the whole batch.
	Transactional bool
	// LocalBackup is true when the executor can take a point-in-time file backup
	// before applying (only local files).
	LocalBackup bool
	// Provider is a short label: "local", "turso", "d1".
	Provider string
}

// AsExecutor returns the Executor view of a connector, or false if it can't
// execute statements.
func AsExecutor(c Connector) (Executor, bool) {
	e, ok := c.(Executor)
	return e, ok
}

// Open parses a DSN and returns the appropriate Connector.
//
// Supported formats:
//
//	path/to/file.db                        — local SQLite file
//	turso://TOKEN@ORG/DBNAME               — Turso (libSQL) database
//	d1://TOKEN@ACCOUNT_ID/DATABASE_ID      — Cloudflare D1 database
func Open(dsn string) (Connector, error) {
	switch {
	case strings.HasPrefix(dsn, "turso://"):
		return openTurso(dsn)
	case strings.HasPrefix(dsn, "d1://"):
		return openD1(dsn)
	default:
		return openFile(dsn)
	}
}

// parseTursoDSN parses turso://TOKEN@ORG/DBNAME into its components.
func parseTursoDSN(dsn string) (token, org, dbName string, err error) {
	// Strip scheme
	rest := strings.TrimPrefix(dsn, "turso://")

	// Split token @ rest
	atIdx := strings.Index(rest, "@")
	if atIdx < 0 {
		return "", "", "", fmt.Errorf("turso DSN must be turso://TOKEN@ORG/DBNAME, got: %s", dsn)
	}
	token = rest[:atIdx]
	rest = rest[atIdx+1:]

	// Split org / dbName
	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return "", "", "", fmt.Errorf("turso DSN must be turso://TOKEN@ORG/DBNAME, got: %s", dsn)
	}
	org = rest[:slashIdx]
	dbName = rest[slashIdx+1:]

	if token == "" || org == "" || dbName == "" {
		return "", "", "", fmt.Errorf("turso DSN missing token, org, or dbname: %s", dsn)
	}
	return token, org, dbName, nil
}
