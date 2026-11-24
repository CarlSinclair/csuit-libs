// Package goosedb applies embedded SQL migrations to a SQLite or MySQL
// database at startup. It is a thin wrapper around goose so every product
// versions its schema the same way: numbered .sql files in an embed.FS, a
// goose_db_version table, one transaction per file where the engine allows it.
//
// Rules this package enforces on purpose:
//   - A database that is ahead of the binary (it records a version the embedded
//     files do not contain) is refused instead of being run against a schema
//     the code does not know. Downgrades fail loudly.
//   - Every failure is returned with the file name and database name so the
//     caller can log it and stop startup. Nothing is swallowed.
//   - CheckNoAlter lets an app test suite reject ALTER statements in its
//     migration files, because live schemas are altered by hand, never by code.
package goosedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/pressly/goose/v3"
)

// Dialect selects the SQL flavour goose uses for its own bookkeeping table.
type Dialect string

const (
	SQLite Dialect = "sqlite3"
	MySQL  Dialect = "mysql"
)

// Config describes one database and where its migrations live.
type Config struct {
	// Name labels the database in logs and errors, for example "cskill" or
	// "ctrends account 11c7a1bb".
	Name string
	// DB is an open handle. The caller closes it.
	DB *sql.DB
	// Dialect is SQLite or MySQL.
	Dialect Dialect
	// FS holds the migration files, usually an embed.FS.
	FS fs.FS
	// Dir is the directory inside FS that holds the .sql files, for example
	// "migrations". Empty means the root of FS.
	Dir string
	// Table overrides goose's version table name. Empty keeps goose_db_version.
	Table string
	// Logf receives one line per applied migration. Nil uses the shared logger.
	Logf func(format string, args ...any)
}

func (c Config) provider() (*goose.Provider, error) {
	var gd goose.Dialect
	switch c.Dialect {
	case SQLite:
		gd = goose.DialectSQLite3
	case MySQL:
		gd = goose.DialectMySQL
	default:
		return nil, fmt.Errorf("migrate %s: unknown dialect %q", c.Name, c.Dialect)
	}
	if c.DB == nil || c.FS == nil {
		return nil, fmt.Errorf("migrate %s: DB and FS are required", c.Name)
	}
	fsys := c.FS
	if c.Dir != "" && c.Dir != "." {
		sub, err := fs.Sub(c.FS, c.Dir)
		if err != nil {
			return nil, fmt.Errorf("migrate %s: open migrations dir %q: %w", c.Name, c.Dir, err)
		}
		fsys = sub
	}
	opts := []goose.ProviderOption{goose.WithDisableGlobalRegistry(true)}
	if c.Table != "" {
		opts = append(opts, goose.WithTableName(c.Table))
	}
	p, err := goose.NewProvider(gd, c.DB, fsys, opts...)
	if err != nil {
		return nil, fmt.Errorf("migrate %s: create provider: %w", c.Name, err)
	}
	return p, nil
}

// Up applies every pending migration in order. It returns an error, and the
// caller must stop startup, when a migration fails or when the database is
// ahead of the embedded files.
func Up(ctx context.Context, c Config) error {
	p, err := c.provider()
	if err != nil {
		return err
	}
	logf := c.Logf
	if logf == nil {
		logf = logger.Info
	}
	current, target, err := p.GetVersions(ctx)
	if err != nil {
		return fmt.Errorf("migrate %s: read schema version: %w", c.Name, err)
	}
	if current > target {
		return fmt.Errorf("migrate %s: database is at schema version %d but this build only knows up to %d; refusing to run an older binary against a newer schema", c.Name, current, target)
	}
	results, err := p.Up(ctx)
	failed := ""
	for _, r := range results {
		if r.Error == nil {
			logf("migrate %s: applied %s in %s", c.Name, r.Source.Path, r.Duration.Round(time.Millisecond))
		} else if failed == "" {
			failed = r.Source.Path
		}
	}
	if err != nil {
		var pe *goose.PartialError
		if errors.As(err, &pe) && pe.Failed != nil && pe.Failed.Source != nil {
			failed = pe.Failed.Source.Path
		}
		if failed != "" {
			return fmt.Errorf("migrate %s: %s: %w", c.Name, failed, err)
		}
		return fmt.Errorf("migrate %s: %w", c.Name, err)
	}
	return nil
}

// Pending reports whether the database has migrations it has not applied yet.
func Pending(ctx context.Context, c Config) (bool, error) {
	p, err := c.provider()
	if err != nil {
		return false, err
	}
	has, err := p.HasPending(ctx)
	if err != nil {
		return false, fmt.Errorf("migrate %s: check pending: %w", c.Name, err)
	}
	return has, nil
}

var alterRe = regexp.MustCompile(`(?i)\bALTER\s+TABLE\b`)

// CheckNoAlter returns an error naming every migration file that contains an
// ALTER TABLE statement outside of a SQL comment. Call it from a test.
func CheckNoAlter(fsys fs.FS, dir string) error {
	if dir == "" {
		dir = "."
	}
	var bad []string
	err := fs.WalkDir(fsys, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return nil
		}
		b, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			code := line
			if j := strings.Index(code, "--"); j >= 0 && !strings.Contains(code[:j], "+goose") {
				code = code[:j]
			}
			if alterRe.MatchString(code) {
				bad = append(bad, fmt.Sprintf("%s:%d", path, i+1))
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan migrations for ALTER: %w", err)
	}
	if len(bad) > 0 {
		return fmt.Errorf("ALTER TABLE is not allowed in migrations, fold the change into the baseline and alter live databases by hand: %s", strings.Join(bad, ", "))
	}
	return nil
}
