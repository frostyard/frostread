package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "frostread.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if d, err := c.Interval(); err != nil || d != DefaultInterval {
		t.Fatalf("interval = %v, %v", d, err)
	}
	if c.NotesFolder != DefaultFolder || c.LodgeURL != "" || c.NotesVault != 0 {
		t.Fatalf("defaults = %+v", c)
	}
	if c.BlobSweep != SweepOn || c.BlobGraceDays != DefaultGraceDays || DefaultGraceDays != 7 {
		t.Fatalf("sweep defaults = %q, %d; want on, 7", c.BlobSweep, c.BlobGraceDays)
	}
}

func TestBlobSweepSettings(t *testing.T) {
	for _, tc := range []struct {
		body string
		mode string
		days int
	}{
		{`{}`, "on", 7},
		{`{"blob_sweep": "dry-run"}`, "dry-run", 7},
		{`{"blob_sweep": "off", "blob_grace_days": 30}`, "off", 30},
		{`{"blob_grace_days": 1}`, "on", 1},
		{`{"blob_grace_days": 365}`, "on", 365},
	} {
		c, err := Load(write(t, tc.body))
		if err != nil {
			t.Fatalf("%s: %v", tc.body, err)
		}
		if c.BlobSweep != tc.mode || c.BlobGraceDays != tc.days {
			t.Fatalf("%s = %q, %d; want %q, %d", tc.body, c.BlobSweep, c.BlobGraceDays, tc.mode, tc.days)
		}
	}
}

func TestFull(t *testing.T) {
	c, err := Load(write(t, `{
		"poll_interval": "45m",
		"lodge_url": "https://lodge.example.ts.net/",
		"notes_folder": "Reading/Articles/",
		"notes_vault": 2
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := c.Interval(); d != 45*time.Minute {
		t.Fatalf("interval = %v", d)
	}
	if c.LodgeURL != "https://lodge.example.ts.net/" || c.NotesFolder != "Reading/Articles/" || c.NotesVault != 2 {
		t.Fatalf("config = %+v", c)
	}
}

func TestRejectsBadValues(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"unknown member", `{"colour": "blue"}`},
		{"short interval", `{"poll_interval": "5m"}`},
		{"long interval", `{"poll_interval": "48h"}`},
		{"not a duration", `{"poll_interval": "often"}`},
		{"http lodge url", `{"lodge_url": "http://lodge.example.ts.net"}`},
		{"lodge url with a path", `{"lodge_url": "https://lodge.example.ts.net/app"}`},
		{"folder without a slash", `{"notes_folder": "Reading"}`},
		{"folder climbing out", `{"notes_folder": "../Reading/"}`},
		{"absolute folder", `{"notes_folder": "/Reading/"}`},
		{"negative vault", `{"notes_vault": -1}`},
		{"unknown sweep mode", `{"blob_sweep": "sometimes"}`},
		{"sweep mode in capitals", `{"blob_sweep": "ON"}`},
		{"negative grace", `{"blob_grace_days": -1}`},
		{"grace too long", `{"blob_grace_days": 366}`},
		{"grace not a number", `{"blob_grace_days": "7"}`},
		{"malformed json", `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(write(t, tc.body)); err == nil {
				t.Fatalf("%s was accepted", tc.body)
			}
		})
	}
}

func TestStateDir(t *testing.T) {
	t.Setenv("STATE_DIRECTORY", "/srv/one:/srv/two")
	if got := StateDir(); got != "/srv/one" {
		t.Fatalf("state dir = %q", got)
	}
	t.Setenv("STATE_DIRECTORY", "")
	if got := StateDir(); got != "/var/lib/frostread" {
		t.Fatalf("state dir = %q", got)
	}
}

func TestToken(t *testing.T) {
	state := t.TempDir()
	if tok, err := Token(state); err != nil || tok != "" {
		t.Fatalf("no token = %q, %v", tok, err)
	}
	good := "lgm_" + strings.Repeat("a", 43)
	if err := SetToken(state, good+"\n"); err != nil {
		t.Fatal(err)
	}
	tok, err := Token(state)
	if err != nil || tok != good {
		t.Fatalf("token = %q, %v", tok, err)
	}
	fi, err := os.Stat(TokenPath(state))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %v, %v", fi.Mode(), err)
	}
	if err := SetToken(state, "nope"); err == nil {
		t.Fatal("a token that is not a lodge token was accepted")
	}
}
