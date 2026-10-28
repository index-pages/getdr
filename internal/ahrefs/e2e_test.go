package ahrefs

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/index-pages/getdr/internal/lookup"
)

// TestLiveAhrefs calls the real Ahrefs endpoint. It runs only when
// AHREFS_KEY is set in the environment.
func TestLiveAhrefs(t *testing.T) {
	key := os.Getenv("AHREFS_KEY")
	if key == "" {
		t.Skip("AHREFS_KEY not set: skipping live Ahrefs e2e test")
	}
	c := &Client{Key: key, Batch: 1000, RPM: 30, Timeout: 30 * time.Second, Retries: 2}
	for _, version := range []string{"dr3", "dr4"} {
		out := c.Fetch(context.Background(), []string{"ahrefs.com", "index-pages.com"}, version, false)
		o := out["ahrefs.com"]
		if o.NoResponse {
			t.Fatalf("%s: no response: %v", version, o.Err)
		}
		if o.DR == nil || *o.DR < 1 || *o.DR > 100 {
			t.Fatalf("%s: ahrefs.com DR %v", version, o.DR)
		}
		t.Logf("%s: ahrefs.com DR %v, index-pages.com DR %v", version, *o.DR, out["index-pages.com"].DR)
	}
	bad := &Client{Key: "invalid-key", Timeout: 30 * time.Second}
	out := bad.Fetch(context.Background(), []string{"ahrefs.com"}, "dr3", false)
	if !errors.Is(out["ahrefs.com"].Err, lookup.ErrKey) {
		t.Fatalf("invalid key: %+v", out["ahrefs.com"])
	}
}
