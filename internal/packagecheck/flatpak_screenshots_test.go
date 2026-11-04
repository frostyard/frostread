package packagecheck

// Software centres show the default screenshot first. This test keeps that
// rule and every metainfo image URL checked before a software centre serves
// the repository file.

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	flatpakMetainfo = "org.frostyard.Frostread.metainfo.xml"
	pngSignature    = "\x89PNG\r\n\x1a\n"
)

type flatpakScreenshotMeta struct {
	Screenshots struct {
		Items []struct {
			Type    string   `xml:"type,attr"`
			Caption []string `xml:"caption"`
			Images  []string `xml:"image"`
		} `xml:"screenshot"`
	} `xml:"screenshots"`
}

func checkScreenshots(meta []byte, root string) []error {
	var doc flatpakScreenshotMeta
	if err := xml.Unmarshal(meta, &doc); err != nil {
		return []error{fmt.Errorf("metainfo XML: %w", err)}
	}

	var errs []error
	screenshots := doc.Screenshots.Items
	if len(screenshots) == 0 {
		errs = append(errs, fmt.Errorf("metainfo has no screenshot"))
		return errs
	}

	if screenshots[0].Type != "default" {
		errs = append(errs, fmt.Errorf("the first screenshot is missing type=\"default\""))
	}
	for i, screenshot := range screenshots[1:] {
		if screenshot.Type == "default" {
			errs = append(errs, fmt.Errorf("screenshot %d also has type=\"default\"", i+2))
		}
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		errs = append(errs, fmt.Errorf("resolve repository root: %w", err))
		return errs
	}

	// Each image is a file in docs/images on main: not another file of the
	// repository, and no path that leaves docs/images once it is cleaned.
	const mainPrefix = "/frostyard/frostread/main/docs/images/"
	imagesDir := filepath.Join(absRoot, "docs", "images")
	for i, screenshot := range screenshots {
		if len(screenshot.Caption) == 0 || strings.TrimSpace(screenshot.Caption[0]) == "" {
			errs = append(errs, fmt.Errorf("screenshot %d has an empty caption", i+1))
		}
		if len(screenshot.Images) == 0 {
			errs = append(errs, fmt.Errorf("screenshot %d has no image", i+1))
		}

		for j, rawURL := range screenshot.Images {
			u, err := url.Parse(strings.TrimSpace(rawURL))
			if err != nil {
				errs = append(errs, fmt.Errorf("screenshot %d image %d URL is invalid: %w", i+1, j+1, err))
				continue
			}
			if u.Scheme != "https" {
				errs = append(errs, fmt.Errorf("screenshot %d image %d URL scheme is %q, want https", i+1, j+1, u.Scheme))
			}
			if u.Host != "raw.githubusercontent.com" {
				errs = append(errs, fmt.Errorf("screenshot %d image %d URL host is %q, want raw.githubusercontent.com", i+1, j+1, u.Host))
			}
			if !strings.HasPrefix(u.Path, mainPrefix) {
				errs = append(errs, fmt.Errorf("screenshot %d image %d URL path %q does not start with %q", i+1, j+1, u.Path, mainPrefix))
				continue
			}

			rest := strings.TrimPrefix(u.Path, mainPrefix)
			target := filepath.Join(imagesDir, filepath.FromSlash(rest))
			rel, err := filepath.Rel(imagesDir, target)
			if err != nil {
				errs = append(errs, fmt.Errorf("screenshot %d image %d path is invalid: %w", i+1, j+1, err))
				continue
			}
			if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				errs = append(errs, fmt.Errorf("screenshot %d image %d path %q is not a file in docs/images", i+1, j+1, u.Path))
				continue
			}

			data, err := os.ReadFile(target)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					errs = append(errs, fmt.Errorf("screenshot %d image %d file does not exist: %s", i+1, j+1, filepath.ToSlash(rel)))
				} else {
					errs = append(errs, fmt.Errorf("screenshot %d image %d file cannot be read: %w", i+1, j+1, err))
				}
				continue
			}
			if !strings.HasPrefix(string(data), pngSignature) {
				errs = append(errs, fmt.Errorf("screenshot %d image %d file is not a PNG: %s", i+1, j+1, filepath.ToSlash(rel)))
			}
		}
	}

	return errs
}

func TestFlatpakMetainfoScreenshots(t *testing.T) {
	meta, err := os.ReadFile(filepath.Join("..", "..", "packaging", "flatpak", flatpakMetainfo))
	if err != nil {
		t.Fatal(err)
	}
	if errs := checkScreenshots(meta, "../.."); len(errs) != 0 {
		for _, err := range errs {
			t.Error(err)
		}
	}
}

func TestFlatpakMetainfoScreenshotsRejects(t *testing.T) {
	root := t.TempDir()
	base := "https://raw.githubusercontent.com/frostyard/frostread/main/docs/images/"

	validPath := filepath.Join(root, "docs", "images", "reading.png")
	if err := os.MkdirAll(filepath.Dir(validPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(validPath, []byte(pngSignature+"valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "images", "not-png.png"), []byte("not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A real PNG elsewhere in the repository, such as the app's icon, is no
	// screenshot.
	iconPath := filepath.Join(root, "web", "src-tauri", "icons", "icon.png")
	if err := os.MkdirAll(filepath.Dir(iconPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(iconPath, []byte(pngSignature+"icon"), 0o644); err != nil {
		t.Fatal(err)
	}

	component := func(body string) string { return "<component>" + body + "</component>" }
	screenshot := func(typ, caption, image string) string {
		var attrs string
		if typ != "" {
			attrs = ` type="` + typ + `"`
		}
		var parts strings.Builder
		parts.WriteString("<screenshot" + attrs + ">")
		if caption != "" {
			parts.WriteString("<caption>" + caption + "</caption>")
		} else {
			parts.WriteString("<caption></caption>")
		}
		if image != "" {
			parts.WriteString("<image>" + image + "</image>")
		}
		parts.WriteString("</screenshot>")
		return parts.String()
	}

	valid := base + "reading.png"
	cases := []struct {
		name string
		meta string
		want string
	}{
		{"no screenshots", component(""), "no screenshot"},
		{"first is not default", component("<screenshots>" + screenshot("", "Reading", valid) + "</screenshots>"), "default"},
		{"another default", component("<screenshots>" + screenshot("default", "Reading", valid) + screenshot("default", "Later", valid) + "</screenshots>"), "default"},
		{"empty caption", component("<screenshots>" + screenshot("default", "", valid) + "</screenshots>"), "caption"},
		{"no image", component("<screenshots>" + screenshot("default", "Reading", "") + "</screenshots>"), "image"},
		{"http URL", component("<screenshots>" + screenshot("default", "Reading", "http://raw.githubusercontent.com/frostyard/frostread/main/docs/images/reading.png") + "</screenshots>"), "scheme"},
		{"another host", component("<screenshots>" + screenshot("default", "Reading", "https://example.com/frostyard/frostread/main/docs/images/reading.png") + "</screenshots>"), "host"},
		{"outside main", component("<screenshots>" + screenshot("default", "Reading", "https://raw.githubusercontent.com/other/repository/main/docs/images/reading.png") + "</screenshots>"), "main/"},
		{"PNG outside docs/images", component("<screenshots>" + screenshot("default", "Reading", "https://raw.githubusercontent.com/frostyard/frostread/main/web/src-tauri/icons/icon.png") + "</screenshots>"), "docs/images"},
		{"path leaves docs/images", component("<screenshots>" + screenshot("default", "Reading", base+"../../web/src-tauri/icons/icon.png") + "</screenshots>"), "docs/images"},
		{"file does not exist", component("<screenshots>" + screenshot("default", "Reading", base+"missing.png") + "</screenshots>"), "does not exist"},
		{"file is not PNG", component("<screenshots>" + screenshot("default", "Reading", base+"not-png.png") + "</screenshots>"), "PNG"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkScreenshots([]byte(tc.meta), root)
			if len(errs) == 0 {
				t.Fatalf("checkScreenshots accepted bad metainfo; want error naming %q", tc.want)
			}
			for _, err := range errs {
				if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
					return
				}
			}
			t.Errorf("errors do not name %q: %v", tc.want, errs)
		})
	}
}
