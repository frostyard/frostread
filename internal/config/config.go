// Package config is readd's configuration, /etc/frostread/frostread.json,
// and its state directory: the database, the blobs and the notes token
// (docs/specs/api.md, Configuration and State).
package config

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultPath is the configuration file, unless READ_CONFIG names another.
const DefaultPath = "/etc/frostread/frostread.json"

// DefaultInterval is a source's poll interval when the configuration
// names none.
const DefaultInterval = 20 * time.Minute

// DefaultFolder is where save-as-note writes unless the configuration
// names another folder.
const DefaultFolder = "Reading/"

// The blob sweep's modes and its grace period (docs/adr/0007-*).
const (
	// SweepOn sweeps on the daily schedule, and removes.
	SweepOn = "on"
	// SweepDryRun sweeps on the schedule, and only logs what it would remove.
	SweepDryRun = "dry-run"
	// SweepOff has no schedule; readctl sweep still works.
	SweepOff = "off"
	// DefaultGraceDays is how long a blob stays unreferenced before it goes.
	DefaultGraceDays = 7
	// MinGraceDays and MaxGraceDays bound blob_grace_days.
	MinGraceDays = 1
	MaxGraceDays = 365
)

// The interval's bounds.
const (
	MinInterval = 15 * time.Minute
	MaxInterval = 24 * time.Hour
)

// Config is frostread.json. Every field may be left out.
type Config struct {
	// PollInterval is a source's interval between polls, such as "20m".
	PollInterval string `json:"poll_interval"`
	// LodgeURL is lodge's address, such as https://lodge.example.ts.net.
	// Links frostread writes into notes point there when it is set.
	LodgeURL string `json:"lodge_url"`
	// NotesFolder is the vault folder save-as-note writes in, ending in /.
	NotesFolder string `json:"notes_folder"`
	// NotesVault is the notes vault's id; save-as-note needs it.
	NotesVault int64 `json:"notes_vault"`
	// BlobSweep is the daily blob sweep's mode: "on" (the default),
	// "dry-run" or "off".
	BlobSweep string `json:"blob_sweep"`
	// BlobGraceDays is how many days a blob has no reference before the
	// sweep removes it: 7 by default, 1 to 365.
	BlobGraceDays int `json:"blob_grace_days"`
}

// Load reads the configuration at path, or READ_CONFIG, or DefaultPath. A
// missing file gives the defaults. A member it does not know is an error.
func Load(path string) (Config, error) {
	if path == "" {
		path = os.Getenv("READ_CONFIG")
	}
	if path == "" {
		path = DefaultPath
	}
	var c Config
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return c, fmt.Errorf("read %s: %w", path, err)
	default:
		if err := json.Unmarshal(b, &c, json.RejectUnknownMembers(true)); err != nil {
			return c, fmt.Errorf("%s: %w", path, err)
		}
	}
	if c.PollInterval == "" {
		c.PollInterval = DefaultInterval.String()
	}
	if c.NotesFolder == "" {
		c.NotesFolder = DefaultFolder
	}
	if c.BlobSweep == "" {
		c.BlobSweep = SweepOn
	}
	if c.BlobGraceDays == 0 {
		c.BlobGraceDays = DefaultGraceDays
	}
	if err := c.checkSweep(); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := c.Interval(); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if c.LodgeURL != "" {
		if err := c.checkLodgeURL(); err != nil {
			return c, fmt.Errorf("%s: %w", path, err)
		}
	}
	if err := c.checkFolder(); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if c.NotesVault < 0 {
		return c, fmt.Errorf("%s: notes_vault %d is not a vault id", path, c.NotesVault)
	}
	return c, nil
}

// Interval is the poll interval, within the contract's bounds.
func (c Config) Interval() (time.Duration, error) {
	d, err := time.ParseDuration(c.PollInterval)
	if err != nil {
		return 0, fmt.Errorf("poll_interval %q is not a duration: %w", c.PollInterval, err)
	}
	if d < MinInterval || d > MaxInterval {
		return 0, fmt.Errorf("poll_interval %q is outside %s to %s", c.PollInterval, MinInterval, MaxInterval)
	}
	return d, nil
}

// checkSweep requires a known mode and a grace period within bounds.
func (c Config) checkSweep() error {
	switch c.BlobSweep {
	case SweepOn, SweepDryRun, SweepOff:
	default:
		return fmt.Errorf("blob_sweep %q is not %q, %q or %q", c.BlobSweep, SweepOn, SweepDryRun, SweepOff)
	}
	if c.BlobGraceDays < MinGraceDays || c.BlobGraceDays > MaxGraceDays {
		return fmt.Errorf("blob_grace_days %d is outside %d to %d", c.BlobGraceDays, MinGraceDays, MaxGraceDays)
	}
	return nil
}

// checkLodgeURL requires an https origin with no path.
func (c Config) checkLodgeURL() error {
	u, err := url.Parse(c.LodgeURL)
	if err != nil || u.Scheme != "https" || u.Host == "" ||
		strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("lodge_url %q is not an https origin, such as https://lodge.example.ts.net", c.LodgeURL)
	}
	return nil
}

// checkFolder requires a vault path that ends in / and cannot climb out.
func (c Config) checkFolder() error {
	f := c.NotesFolder
	if !strings.HasSuffix(f, "/") || strings.HasPrefix(f, "/") || strings.Contains(f, `\`) {
		return fmt.Errorf("notes_folder %q must be a relative path ending in /", f)
	}
	for _, seg := range strings.Split(strings.TrimSuffix(f, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("notes_folder %q has an empty or climbing segment", f)
		}
	}
	return nil
}

// StateDir is readd's state directory: STATE_DIRECTORY, which systemd
// sets from StateDirectory=, else /var/lib/frostread.
func StateDir() string {
	if d := os.Getenv("STATE_DIRECTORY"); d != "" {
		d, _, _ = strings.Cut(d, ":")
		return d
	}
	return "/var/lib/frostread"
}

// TokenPath is where the notes MCP token is kept.
func TokenPath(state string) string { return filepath.Join(state, "tokens", "notes") }

// Token reads the notes token; "" when there is none.
func Token(state string) (string, error) {
	b, err := os.ReadFile(TokenPath(state))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("notes token: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

var tokenPattern = regexp.MustCompile(`^lgm_[A-Za-z0-9_-]{43}$`)

// SetToken writes the notes token, 0600, replacing any before it.
func SetToken(state, tok string) error {
	tok = strings.TrimSpace(tok)
	if !tokenPattern.MatchString(tok) {
		return errors.New("that is not a lodge MCP token (lgm_ and 43 characters)")
	}
	dir := filepath.Dir(TokenPath(state))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(tok + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), TokenPath(state))
}
