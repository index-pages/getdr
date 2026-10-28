package cache

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func f(v float64) *float64 { return &v }

func open(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "sub", "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInsertHistoryVersions(t *testing.T) {
	db := open(t)
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := db.Insert("dr3", []Row{
		{Domain: "a.com", DR: f(40), CheckedAt: t0},
		{Domain: "a.com", DR: f(45), CheckedAt: t0.Add(24 * time.Hour)},
		{Domain: "b.com", DR: nil, CheckedAt: t0},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Insert("dr4", []Row{{Domain: "a.com", DR: f(12.5), CheckedAt: t0}}); err != nil {
		t.Fatal(err)
	}
	// Duplicate measurement (same checked_at) is skipped.
	if err := db.Insert("dr3", []Row{{Domain: "a.com", DR: f(45), CheckedAt: t0.Add(24 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	h, err := db.History("dr3", []string{"a.com", "b.com", "c.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(h["a.com"]) != 2 || *h["a.com"][0].DR != 40 || *h["a.com"][1].DR != 45 {
		t.Fatalf("a.com history %+v", h["a.com"])
	}
	if len(h["b.com"]) != 1 || h["b.com"][0].DR != nil {
		t.Fatalf("b.com history %+v", h["b.com"])
	}
	if _, ok := h["c.com"]; ok {
		t.Fatal("c.com must be absent")
	}
	h4, _ := db.History("dr4", []string{"a.com"})
	if len(h4["a.com"]) != 1 || *h4["a.com"][0].DR != 12.5 {
		t.Fatalf("dr4 history %+v", h4)
	}
	st, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if st.Rows != 4 || st.Domains != 2 || st.Versions["dr3"] != 2 || st.Versions["dr4"] != 1 || st.SizeBytes == 0 {
		t.Fatalf("stats %+v", st)
	}
}

func TestHistoryManyDomains(t *testing.T) {
	db := open(t)
	var rows []Row
	var names []string
	now := time.Now()
	for i := 0; i < 1234; i++ {
		d := "d" + string(rune('a'+i%26)) + time.Duration(i).String() + ".com"
		names = append(names, d)
		rows = append(rows, Row{Domain: d, DR: f(float64(i % 100)), CheckedAt: now})
	}
	if err := db.Insert("dr3", rows); err != nil {
		t.Fatal(err)
	}
	h, err := db.History("dr3", names)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 1234 {
		t.Fatalf("got %d domains", len(h))
	}
}

func TestPurgeKeepsLatest(t *testing.T) {
	db := open(t)
	now := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	_ = db.Insert("dr3", []Row{
		{Domain: "a.com", DR: f(1), CheckedAt: now.Add(-90 * day)},
		{Domain: "a.com", DR: f(2), CheckedAt: now.Add(-60 * day)},
		{Domain: "a.com", DR: f(3), CheckedAt: now.Add(-1 * day)},
		{Domain: "old.com", DR: f(5), CheckedAt: now.Add(-100 * day)},
		{Domain: "old.com", DR: f(6), CheckedAt: now.Add(-50 * day)},
	})
	_ = db.Insert("dr4", []Row{{Domain: "a.com", DR: f(9), CheckedAt: now.Add(-90 * day)}})
	n, err := db.Purge(30*day, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("purged %d rows, want 3", n)
	}
	h, _ := db.History("dr3", []string{"a.com", "old.com"})
	if len(h["a.com"]) != 1 || *h["a.com"][0].DR != 3 {
		t.Fatalf("a.com %+v", h["a.com"])
	}
	if len(h["old.com"]) != 1 || *h["old.com"][0].DR != 6 {
		t.Fatalf("old.com must keep its latest check: %+v", h["old.com"])
	}
	h4, _ := db.History("dr4", []string{"a.com"})
	if len(h4["a.com"]) != 1 {
		t.Fatalf("dr4 latest must stay: %+v", h4)
	}
	if _, err := db.Clear(); err != nil {
		t.Fatal(err)
	}
	if st, _ := db.Stats(); st.Rows != 0 {
		t.Fatalf("rows after clear: %d", st.Rows)
	}
}

func TestSharedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var wg sync.WaitGroup
	for i, db := range []*DB{a, b, a, b} {
		wg.Add(1)
		go func(i int, db *DB) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if err := db.Insert("dr3", []Row{{Domain: "x.com", DR: f(float64(j)), CheckedAt: time.Unix(int64(i*100+j), 0)}}); err != nil {
					t.Error(err)
					return
				}
			}
		}(i, db)
	}
	wg.Wait()
	if st, _ := b.Stats(); st.Rows != 80 {
		t.Fatalf("rows %d, want 80", st.Rows)
	}
}
