// Package migrate 提供最小但严格的 MySQL 版本化迁移器。
package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var filePattern = regexp.MustCompile(`^(\d+)_([a-zA-Z0-9_-]+)\.sql$`)

type Migration struct {
	Version  int64
	Name     string
	Path     string
	SQL      string
	Checksum string
}

type State struct {
	Version   int64
	Name      string
	Checksum  string
	Dirty     bool
	AppliedAt *time.Time
}

func LoadDir(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migration directory %q: %w", dir, err)
	}
	migrations := make([]Migration, 0, len(entries))
	seen := make(map[int64]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := filePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", entry.Name(), err)
		}
		if old, ok := seen[version]; ok {
			return nil, fmt.Errorf("duplicate migration version %d: %s and %s", version, old, entry.Name())
		}
		path := filepath.Join(dir, entry.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", path, err)
		}
		if strings.TrimSpace(string(body)) == "" {
			return nil, fmt.Errorf("migration %q is empty", path)
		}
		sum := sha256.Sum256(body)
		migrations = append(migrations, Migration{Version: version, Name: match[2], Path: path, SQL: string(body), Checksum: hex.EncodeToString(sum[:])})
		seen[version] = entry.Name()
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	if len(migrations) == 0 {
		return nil, fmt.Errorf("no migration files found in %q", dir)
	}
	return migrations, nil
}

type Runner struct {
	db  *sql.DB
	dir string
}

func New(db *sql.DB, dir string) *Runner { return &Runner{db: db, dir: dir} }

func (r *Runner) ensureTable(ctx context.Context) error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
version BIGINT NOT NULL PRIMARY KEY,
name VARCHAR(190) NOT NULL,
checksum CHAR(64) NOT NULL,
dirty TINYINT(1) NOT NULL DEFAULT 1,
applied_at DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
	if _, err := r.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func (r *Runner) Status(ctx context.Context) ([]State, error) {
	if err := r.ensureTable(ctx); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT version,name,checksum,dirty,applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("query migration status: %w", err)
	}
	defer rows.Close()
	states := make([]State, 0)
	for rows.Next() {
		var state State
		if err := rows.Scan(&state.Version, &state.Name, &state.Checksum, &state.Dirty, &state.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan migration status: %w", err)
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migration status: %w", err)
	}
	return states, nil
}

// Up 按版本顺序升级。MySQL DDL 会隐式提交，因此先写 dirty=true；中途失败后必须人工检查再处理，服务不会带病启动。
func (r *Runner) Up(ctx context.Context) error {
	migrations, err := LoadDir(r.dir)
	if err != nil {
		return err
	}
	states, err := r.Status(ctx)
	if err != nil {
		return err
	}
	applied := make(map[int64]State, len(states))
	for _, state := range states {
		if state.Dirty {
			return fmt.Errorf("migration %03d_%s is dirty; inspect the partially applied DDL before retrying", state.Version, state.Name)
		}
		applied[state.Version] = state
	}
	for _, migration := range migrations {
		if state, ok := applied[migration.Version]; ok {
			if state.Name != migration.Name || state.Checksum != migration.Checksum {
				return fmt.Errorf("migration %03d changed after it was applied (database=%s, file=%s)", migration.Version, state.Checksum, migration.Checksum)
			}
			continue
		}
		if _, err := r.db.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,checksum,dirty) VALUES(?,?,?,1)`, migration.Version, migration.Name, migration.Checksum); err != nil {
			return fmt.Errorf("mark migration %03d dirty: %w", migration.Version, err)
		}
		if _, err := r.db.ExecContext(ctx, migration.SQL); err != nil {
			return fmt.Errorf("apply migration %03d_%s: %w", migration.Version, migration.Name, err)
		}
		if _, err := r.db.ExecContext(ctx, `UPDATE schema_migrations SET dirty=0,applied_at=UTC_TIMESTAMP(6) WHERE version=?`, migration.Version); err != nil {
			return fmt.Errorf("mark migration %03d clean: %w", migration.Version, err)
		}
	}
	return nil
}
