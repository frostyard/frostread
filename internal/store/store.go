// Package store is readd's SQLite database: sources, items, blobs, the
// full-text index and the daemon's settings (docs/design/overview.md). It
// is on local disk only: WAL needs shared memory, so a network share is
// refused by the caller.
//
// Writes go through DB.Tx, which serializes them. Reads may run
// concurrently. Migrations are numbered SQL files, applied in order, and
// recorded in the database's user_version.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrations embed.FS

var (
	// ErrNotFound means the row does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrConflict means a uniqueness rule rejected the write.
	ErrConflict = errors.New("store: conflict")
)

// TimeFormat is how times are stored: UTC, fixed width, lexically sortable.
const TimeFormat = "2006-01-02T15:04:05.000Z"

// DB is readd's database.
type DB struct {
	db      *sql.DB
	writeMu sync.Mutex

	// Now is the clock for stored timestamps; tests replace it.
	Now func() time.Time
}

// Open opens or creates the database at path and applies pending
// migrations. path must not live on a network share.
func Open(ctx context.Context, path string) (*DB, error) {
	if strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("open database: path %q contains ? or #", path)
	}
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)&_txlock=immediate"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB.SetMaxOpenConns(8)
	d := &DB{db: sqlDB, Now: time.Now}
	if err := d.migrate(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	return d, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// SchemaVersion is the applied migration count.
func (d *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := d.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v)
	return v, err
}

// migration is one file in migrations/.
type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads migrations/NNNN_name.sql in version order, which
// must run 1, 2, 3 without a gap.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		prefix, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(prefix)
		if !ok || err != nil || !strings.HasSuffix(e.Name(), ".sql") {
			return nil, fmt.Errorf("migration %s: name must be NNNN_name.sql", e.Name())
		}
		body, err := fs.ReadFile(migrations, "migrations/"+e.Name())
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: v, name: e.Name(), sql: string(body)})
	}
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %s: versions must run 1, 2, 3 without gaps", m.name)
		}
	}
	return ms, nil
}

// migrate applies the migrations after the database's user_version.
func (d *DB) migrate(ctx context.Context) error {
	ms, err := loadMigrations()
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	var current int
	if err := d.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if current > len(ms) {
		return fmt.Errorf("migrate: database schema %d is newer than this readd (%d)", current, len(ms))
	}
	for _, m := range ms[current:] {
		tx, err := d.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migrate %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate %s: %w", m.name, err)
		}
		// PRAGMA does not take a bound parameter.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate %s: %w", m.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrate %s: %w", m.name, err)
		}
	}
	return nil
}

// Tx runs fn in one transaction, one writer at a time. A conflict from fn
// is returned wrapped with ErrConflict.
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Secret returns the 32-byte secret the content MACs use, making it on the
// first call.
func (d *DB) Secret(ctx context.Context) ([]byte, error) {
	s, err := d.setting(ctx, "content_secret")
	if err != nil || s != "" {
		if err != nil {
			return nil, err
		}
		b, err := decodeHex(s)
		if err != nil || len(b) != 32 {
			return nil, errors.New("store: the content secret is malformed")
		}
		return b, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("make the content secret: %w", err)
	}
	err = d.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('content_secret', ?)
			ON CONFLICT (key) DO NOTHING`, encodeHex(b))
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("store the content secret: %w", err)
	}
	// Another caller may have stored one first; use what the database has.
	s, err = d.setting(ctx, "content_secret")
	if err != nil {
		return nil, err
	}
	if b2, err := decodeHex(s); err == nil && len(b2) == 32 {
		return b2, nil
	}
	return nil, errors.New("store: the content secret is malformed")
}

func (d *DB) setting(ctx context.Context, key string) (string, error) {
	var v string
	err := d.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read setting %s: %w", key, err)
	}
	return v, nil
}

// timeString formats t for storage; the zero time is stored as NULL.
func timeString(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(TimeFormat)
}

// parseTime reads a stored time; an empty or NULL value is the zero time.
func parseTime(s sql.NullString) time.Time {
	if !s.Valid || s.String == "" {
		return time.Time{}
	}
	t, err := time.Parse(TimeFormat, s.String)
	if err != nil {
		return time.Time{}
	}
	return t
}

func encodeHex(b []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hex[c>>4]
		out[i*2+1] = hex[c&0x0f]
	}
	return string(out)
}

func decodeHex(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errors.New("odd length")
	}
	out := make([]byte, len(s)/2)
	for i := range out {
		v, err := strconv.ParseUint(s[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, err
		}
		out[i] = byte(v)
	}
	return out, nil
}
