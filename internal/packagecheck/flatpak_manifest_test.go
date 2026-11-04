package packagecheck

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// flatpakManifest is the manifest fields that this package check owns.
type flatpakManifest struct {
	AppID      string   `json:"app-id"`
	Command    string   `json:"command"`
	FinishArgs []string `json:"finish-args"`
	Modules    []struct {
		Name    string `json:"name"`
		Sources []any  `json:"sources"`
	} `json:"modules"`
}

// TestFlatpakManifest verifies the standalone app's isolation and its named
// payload files. Other checks own the desktop file and metainfo content.
func TestFlatpakManifest(t *testing.T) {
	path := filepath.Join("..", "..", "packaging", "flatpak", "org.frostyard.Frostread.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m flatpakManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if m.AppID != "org.frostyard.Frostread" {
		t.Errorf("app-id = %q, want org.frostyard.Frostread", m.AppID)
	}
	if m.Command != "frostread" {
		t.Errorf("command = %q, want frostread", m.Command)
	}

	wantArgs := []string{
		"--socket=wayland",
		"--socket=fallback-x11",
		"--share=ipc",
		"--device=dri",
		"--share=network",
	}
	if len(m.FinishArgs) != len(wantArgs) {
		t.Errorf("finish-args = %v, want %v", m.FinishArgs, wantArgs)
	} else {
		for _, want := range wantArgs {
			if !slices.Contains(m.FinishArgs, want) {
				t.Errorf("finish-args = %v, lacks %q", m.FinishArgs, want)
			}
		}
	}

	if len(m.Modules) != 2 {
		t.Fatalf("modules = %d, want readd then frostread", len(m.Modules))
	}
	if m.Modules[0].Name != "readd" || m.Modules[1].Name != "frostread" {
		t.Errorf("module names = %q, %q; want readd, frostread", m.Modules[0].Name, m.Modules[1].Name)
	}

	base := filepath.Join("..", "..", "packaging", "flatpak")
	var sourceNames []string
	for _, module := range m.Modules {
		for _, source := range module.Sources {
			switch value := source.(type) {
			case string:
				sourceNames = append(sourceNames, filepath.Base(value))
			case map[string]any:
				if path, ok := value["path"].(string); ok {
					sourceNames = append(sourceNames, filepath.Base(path))
				}
			}
		}
	}
	for _, name := range []string{
		"go-sources.json",
		"cargo-sources.json",
		"node-sources.json",
		"frostread.sh",
		"org.frostyard.Frostread.desktop",
		"org.frostyard.Frostread.metainfo.xml",
	} {
		if !slices.Contains(sourceNames, name) {
			t.Errorf("manifest does not name source %s", name)
		}
		if _, err := os.Stat(filepath.Join(base, name)); err != nil {
			t.Errorf("manifest source %s is missing: %v", name, err)
		}
	}

	// node-sources.json fills an offline pnpm store, and its own shell source
	// names that store only in a pnpm-workspace.yaml at the module's root.
	// pnpm runs in web/, so the store must be named in web/'s file after it,
	// or pnpm install --offline finds no tarball.
	nodeAt, storeAt := -1, -1
	for i, source := range m.Modules[1].Sources {
		switch value := source.(type) {
		case string:
			if value == "node-sources.json" {
				nodeAt = i
			}
		case map[string]any:
			if value["type"] != "shell" {
				continue
			}
			commands, _ := value["commands"].([]any)
			for _, c := range commands {
				if c, ok := c.(string); ok && strings.Contains(c, "storeDir: $PWD/flatpak-node/pnpm-store") && strings.HasSuffix(c, ">> web/pnpm-workspace.yaml") {
					storeAt = i
				}
			}
		}
	}
	if nodeAt < 0 || storeAt < nodeAt {
		t.Errorf("frostread module: node-sources.json at %d, web/pnpm-workspace.yaml's storeDir at %d; want the storeDir after node-sources.json", nodeAt, storeAt)
	}
}
