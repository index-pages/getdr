package ahrefs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/index-pages/getdr/internal/lookup"
	"github.com/index-pages/getdr/internal/target"
)

func liveClient(t *testing.T) *Client {
	key := os.Getenv("AHREFS_KEY")
	if key == "" {
		t.Skip("AHREFS_KEY not set: skipping live Ahrefs e2e test")
	}
	return &Client{Key: key, Batch: 1000, RPM: 30, Timeout: 30 * time.Second, Retries: 2}
}

func drString(o lookup.Outcome) string {
	if o.DR == nil {
		return "null"
	}
	return fmt.Sprint(*o.DR)
}

// TestLiveAhrefs calls the real Ahrefs endpoint. It runs only when
// AHREFS_KEY is set in the environment.
func TestLiveAhrefs(t *testing.T) {
	c := liveClient(t)
	for _, version := range []string{"dr3", "dr4"} {
		out := c.Fetch(context.Background(), []string{"ahrefs.com", "index-pages.com"}, version, false)
		o := out["ahrefs.com"]
		if o.NoResponse {
			t.Fatalf("%s: no response: %v", version, o.Err)
		}
		if o.DR == nil || *o.DR < 1 || *o.DR > 100 {
			t.Fatalf("%s: ahrefs.com DR %s", version, drString(o))
		}
		t.Logf("%s: ahrefs.com DR %s, index-pages.com DR %s", version, drString(o), drString(out["index-pages.com"]))
	}
	bad := &Client{Key: "invalid-key", Timeout: 30 * time.Second}
	out := bad.Fetch(context.Background(), []string{"ahrefs.com"}, "dr3", false)
	if !errors.Is(out["ahrefs.com"].Err, lookup.ErrKey) {
		t.Fatalf("invalid key: %+v", out["ahrefs.com"])
	}
}

// TestLiveInvalidAndIDN checks that one invalid target does not sink the
// batch and that punycode input matches Ahrefs' Unicode echo.
func TestLiveInvalidAndIDN(t *testing.T) {
	c := liveClient(t)
	targets := []string{"ahrefs.com", "localhost", "a..com", "github.com", target.Normalize("xn--e1afmkfd.xn--p1ai")}
	out := c.Fetch(context.Background(), targets, "dr3", false)
	for _, k := range []string{"localhost", "a..com"} {
		if o := out[k]; !o.Invalid {
			t.Errorf("%s must be invalid: %+v", k, o)
		}
	}
	for _, k := range []string{"ahrefs.com", "github.com", "пример.рф"} {
		o := out[k]
		if o.Invalid || o.NoResponse || o.DR == nil {
			t.Errorf("%s: %+v", k, o)
		}
		t.Logf("%s DR %s", k, drString(o))
	}
	raw := c.Fetch(context.Background(), []string{"xn--e1afmkfd.xn--p1ai"}, "dr3", false)
	if o := raw["xn--e1afmkfd.xn--p1ai"]; o.DR == nil {
		t.Errorf("raw punycode must match the Unicode echo: %+v", o)
	}
}
