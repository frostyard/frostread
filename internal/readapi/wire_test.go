package readapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	netpkg "net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/lodge/kit/rpctest"

	"github.com/frostyard/frostread/api"
	"github.com/frostyard/frostread/internal/blobs"
	"github.com/frostyard/frostread/internal/config"
	"github.com/frostyard/frostread/internal/content"
	"github.com/frostyard/frostread/internal/engine"
	"github.com/frostyard/frostread/internal/readapi"
	"github.com/frostyard/frostread/internal/rpcserver"
	"github.com/frostyard/frostread/internal/store"
)

// items.render's answer, as the line the socket carries, stays under the
// 12 MiB bound for an article with 20 MiB of images, packed close to the
// bound: with an integer id, and with the longest string id the envelope's
// allowance holds (ADR-0008,
// The answer stays under the transport's limit; docs/specs/api.md,
// Rendering for the desktop).
func TestRenderAnswerOnTheWire(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	b := blobs.New(filepath.Join(dir, "blobs"))
	it, err := db.SaveLink(t.Context(), "https://example.com/pictures", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	// 200 distinct images of 100 KiB: each data: URI takes about 133 KiB,
	// so the page fills to within one image of the bound, and the rest are
	// left out.
	const images = 200
	var html strings.Builder
	html.WriteString("<p>Many pictures.</p>")
	var stored []store.Blob
	for i := range images {
		data := bytes.Repeat([]byte{byte(i)}, 100<<10)
		hash, err := b.Put(t.Context(), bytes.NewReader(data), "image/png")
		if err != nil {
			t.Fatal(err)
		}
		html.WriteString(`<p><img src="blob:` + hash + `" alt="A picture"></p>`)
		stored = append(stored, store.Blob{Hash: hash, Mime: "image/png", Size: int64(len(data))})
	}
	if err := db.SetExtraction(t.Context(), it.ID, store.Extraction{Title: "Pictures", Text: "Many pictures.",
		ContentHTML: html.String(), State: store.ExtractDone, Images: stored}); err != nil {
		t.Fatal(err)
	}

	eng := engine.New(engine.Options{Store: db, Blobs: b,
		Config: config.Config{PollInterval: "20m", NotesFolder: "Reading/"}, Interval: 20 * time.Minute, Version: "test"})
	srv := rpcserver.New(rpcserver.Options{Name: "readd test"})
	router, err := api.NewRouter(api.Services{RPC: srv, Read: readapi.New(eng, nil), Events: srv})
	if err != nil {
		t.Fatal(err)
	}
	run := rpctest.Serve(t, func(ctx context.Context, ln netpkg.Listener) error { return srv.Serve(ctx, ln, router) })
	nc, err := netpkg.Dial("unix", run.Socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nc.Close() })
	r := bufio.NewReaderSize(nc, 64<<10)
	call := func(id, method, params string) []byte {
		t.Helper()
		if _, err := nc.Write([]byte(`{"jsonrpc":"2.0","id":` + id + `,"method":"` + method + `","params":` + params + "}\n")); err != nil {
			t.Fatal(err)
		}
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		return line
	}
	call("0", "rpc.hello", `{"protocol":1,"client":"test"}`)

	// The longest string id the allowance holds: 990 encoded bytes.
	longID := `"` + strings.Repeat("i", content.MaxRequestID-2) + `"`
	for _, id := range []string{"1", longID} {
		line := call(id, "items.render", `{"id":`+itoa(it.ID)+`}`)
		if len(line) >= content.DefaultLimit {
			t.Fatalf("id %.10s…: the answer takes %d bytes on the wire, want under %d", id, len(line), content.DefaultLimit)
		}
		var reply struct {
			ID     jsontext.Value  `json:"id"`
			Result api.ItemRender  `json:"result"`
			Error  *jsontext.Value `json:"error"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(line), &reply); err != nil {
			t.Fatal(err)
		}
		if reply.Error != nil {
			t.Fatalf("id %.10s…: error %s", id, *reply.Error)
		}
		if string(reply.ID) != id {
			t.Fatalf("id %.10s…: the answer's id is %.10s…", id, reply.ID)
		}
		inlined := strings.Count(reply.Result.HTML, "data:image/png;base64,")
		if reply.Result.OmittedImages == 0 || inlined+reply.Result.OmittedImages != images {
			t.Fatalf("id %.10s…: %d inlined and %d omitted, want %d in all and some omitted",
				id, inlined, reply.Result.OmittedImages, images)
		}
		// The page is packed: one more image would not have fitted.
		if len(line) < content.DefaultLimit-150<<10 {
			t.Fatalf("id %.10s…: the answer takes %d bytes, not close to the bound", id, len(line))
		}
	}

	// One byte over the allowance: readd refuses the request before the kit
	// dispatches it, so no answer passes the bound, whatever the id.
	over := `"` + strings.Repeat("i", content.MaxRequestID-1) + `"`
	line := call(over, "items.render", `{"id":`+itoa(it.ID)+`}`)
	var refused struct {
		ID    jsontext.Value `json:"id"`
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(line), &refused); err != nil {
		t.Fatal(err)
	}
	if refused.Error == nil || refused.Error.Code != int(api.CodeInvalidRequest) {
		t.Fatalf("an id of %d bytes = %s, want invalidRequest", len(over), line)
	}
	if string(refused.ID) != "null" {
		t.Fatalf("an id of %d bytes answered id %s, want null", len(over), refused.ID)
	}
	// The connection lives on: the next call is served, still in the bound.
	if next := call("7", "items.render", `{"id":`+itoa(it.ID)+`}`); len(next) >= content.DefaultLimit {
		t.Fatalf("the call after a refusal takes %d bytes, want under %d", len(next), content.DefaultLimit)
	}
}
