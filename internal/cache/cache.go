// Package cache stores the history of Domain Rating checks in SQLite.
//
// Every check is a row in checks(domain, version, dr, checked_at); dr is NULL
// when Ahrefs has no data for the domain. Rows of different DR versions never
// mix. The database runs in WAL mode with a busy timeout so the CLI and
// getdr serve can share one file.
package cache

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// Check is one stored measurement.
type Check struct {
	DR        *float64
	CheckedAt time.Time
}

// Row is a measurement to insert.
type Row struct {
	Domain    string
	DR        *float64
	CheckedAt time.Time
}

// DB is an open cache.
type DB struct {
	db   *sql.DB
	path string
}

const schema = `
CREATE TABLE IF NOT EXISTS checks (
	domain     TEXT    NOT NULL,
	version    TEXT    NOT NULL,
	dr         REAL,
	checked_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS checks_domain_version ON checks(domain, version, checked_at);
`

// Open opens (creating if needed) the cache database at path.
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("cache path is empty (set cache.path or GETDR_CACHE_PATH)")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("cache: %v", err)
	}
	dsn := path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("cache %s: %v", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("cache %s: %v", path, err)
	}
	return &DB{db: db, path: path}, nil
}

// Close closes the database.
func (c *DB) Close() error { return c.db.Close() }

// Path returns the database file path.
func (c *DB) Path() string { return c.path }

const chunk = 500

// History returns all stored checks for the given domains and version,
// oldest first. Domains without checks are absent from the map.
func (c *DB) History(version string, domains []string) (map[string][]Check, error) {
	out := make(map[string][]Check, len(domains))
	for start := 0; start < len(domains); start += chunk {
		part := domains[start:min(start+chunk, len(domains))]
		args := make([]any, 0, len(part)+1)
		args = append(args, version)
		for _, d := range part {
			args = append(args, d)
		}
		q := `SELECT domain, dr, checked_at FROM checks WHERE version = ? AND domain IN (?` +
			strings.Repeat(",?", len(part)-1) + `) ORDER BY domain, checked_at, rowid`
		rows, err := c.db.Query(q, args...)
		if err != nil {
			return nil, fmt.Errorf("cache: %v", err)
		}
		for rows.Next() {
			var (
				domain string
				dr     sql.NullFloat64
				ts     int64
			)
			if err := rows.Scan(&domain, &dr, &ts); err != nil {
				rows.Close()
				return nil, fmt.Errorf("cache: %v", err)
			}
			ch := Check{CheckedAt: time.Unix(ts, 0).UTC()}
			if dr.Valid {
				v := dr.Float64
				ch.DR = &v
			}
			out[domain] = append(out[domain], ch)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("cache: %v", err)
		}
	}
	return out, nil
}

// Insert stores measurements for one version in a single transaction. A row
// identical in domain, version and checked_at to an existing one is skipped,
// so re-storing a measurement relayed by a getdr server does not duplicate it.
func (c *DB) Insert(version string, rows []Row) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("cache: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO checks(domain, version, dr, checked_at)
		SELECT ?1, ?2, ?3, ?4 WHERE NOT EXISTS (
			SELECT 1 FROM checks WHERE domain = ?1 AND version = ?2 AND checked_at = ?4)`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("cache: %v", err)
	}
	for _, r := range rows {
		var dr any
		if r.DR != nil {
			dr = *r.DR
		}
		if _, err := stmt.Exec(r.Domain, version, dr, r.CheckedAt.Unix()); err != nil {
			stmt.Close()
			tx.Rollback()
			return fmt.Errorf("cache: %v", err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cache: %v", err)
	}
	return nil
}

// Stats describes the cache contents.
type Stats struct {
	Path      string           `json:"path"`
	SizeBytes int64            `json:"size_bytes"`
	Rows      int64            `json:"rows"`
	Domains   int64            `json:"domains"`
	Versions  map[string]int64 `json:"versions"`
	Oldest    *time.Time       `json:"oldest"`
	Newest    *time.Time       `json:"newest"`
}

// Stats returns row counts and the on-disk size (database plus WAL).
func (c *DB) Stats() (Stats, error) {
	s := Stats{Path: c.path, Versions: map[string]int64{}}
	var oldest, newest sql.NullInt64
	err := c.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT domain), MIN(checked_at), MAX(checked_at) FROM checks`).
		Scan(&s.Rows, &s.Domains, &oldest, &newest)
	if err != nil {
		return s, fmt.Errorf("cache: %v", err)
	}
	if oldest.Valid {
		o, n := time.Unix(oldest.Int64, 0).UTC(), time.Unix(newest.Int64, 0).UTC()
		s.Oldest, s.Newest = &o, &n
	}
	rows, err := c.db.Query(`SELECT version, COUNT(DISTINCT domain) FROM checks GROUP BY version ORDER BY version`)
	if err != nil {
		return s, fmt.Errorf("cache: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		var n int64
		if err := rows.Scan(&v, &n); err != nil {
			return s, fmt.Errorf("cache: %v", err)
		}
		s.Versions[v] = n
	}
	for _, suffix := range []string{"", "-wal"} {
		if fi, err := os.Stat(c.path + suffix); err == nil {
			s.SizeBytes += fi.Size()
		}
	}
	return s, rows.Err()
}

// Clear deletes every row.
func (c *DB) Clear() (int64, error) {
	res, err := c.db.Exec(`DELETE FROM checks`)
	if err != nil {
		return 0, fmt.Errorf("cache: %v", err)
	}
	n, _ := res.RowsAffected()
	if _, err := c.db.Exec(`VACUUM`); err != nil {
		return n, fmt.Errorf("cache: %v", err)
	}
	return n, nil
}

// Purge deletes checks older than maxAge, except that the latest check of
// every (domain, version) is always kept.
func (c *DB) Purge(maxAge time.Duration, now time.Time) (int64, error) {
	cutoff := now.Add(-maxAge).Unix()
	res, err := c.db.Exec(`DELETE FROM checks WHERE checked_at < ? AND EXISTS (
		SELECT 1 FROM checks n WHERE n.domain = checks.domain AND n.version = checks.version
		AND (n.checked_at > checks.checked_at OR (n.checked_at = checks.checked_at AND n.rowid > checks.rowid)))`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("cache: %v", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
