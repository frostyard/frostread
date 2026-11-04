package packagecheck

import (
	"context"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestBackgroundHelperNoBus verifies that the helper fails fast, exits 0
// and writes its error when it cannot ask the portal: here no session bus,
// or, on a machine without PyGObject, no PyGObject.
func TestBackgroundHelperNoBus(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}

	state := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	script, err := filepath.Abs("../../packaging/flatpak/request-background.py")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, python, script)
	cmd.Env = append(os.Environ(),
		"XDG_STATE_HOME="+state,
		"DBUS_SESSION_BUS_ADDRESS=unix:path="+filepath.Join(t.TempDir(), "no-bus"),
	)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the helper waited for the 600-second portal timeout: %v", ctx.Err())
	}
	if err != nil {
		t.Fatalf("the helper did not exit 0: %v\n%s", err, out)
	}

	path := filepath.Join(state, "frostread", "background.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v\n%s", path, err, out)
	}
	var answer struct {
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		t.Fatalf("%s is not JSON: %v", path, err)
	}
	if answer.Error == nil {
		t.Fatalf("%s has no error; want a non-null error", path)
	}
}
