//go:build live

package live

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frostyard/lodge/kit/lodgeapp"

	"github.com/frostyard/frostread/api"
)

// The checks need the running development build and tokens. They read
// LODGE_ROOT, READ_LIVE_TOKEN_READ, READ_LIVE_TOKEN_WRITE,
// READ_LIVE_TOKEN_BAD and READ_LIVE_SOCKET from the environment, and skip
// when one is missing. No token is ever printed.
func setup(t *testing.T) (env lodgeapp.Env, read, write, bad, socket string) {
	t.Helper()
	root := os.Getenv("LODGE_ROOT")
	read = os.Getenv("READ_LIVE_TOKEN_READ")
	write = os.Getenv("READ_LIVE_TOKEN_WRITE")
	bad = os.Getenv("READ_LIVE_TOKEN_BAD")
	socket = os.Getenv("READ_LIVE_SOCKET")
	if root == "" || read == "" || write == "" || bad == "" || socket == "" {
		t.Skip("set LODGE_ROOT, READ_LIVE_TOKEN_READ, READ_LIVE_TOKEN_WRITE, READ_LIVE_TOKEN_BAD and READ_LIVE_SOCKET")
	}
	return lodgeapp.Env{Root: root}, read, write, bad, socket
}

// connect opens an MCP session over lodge's agent socket with token.
func connect(ctx context.Context, env lodgeapp.Env, token string) (*mcp.ClientSession, error) {
	c := mcp.NewClient(&mcp.Implementation{Name: "read-live", Version: "1"}, nil)
	return c.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: lodgeapp.AgentURL("read"), HTTPClient: env.AgentClient(token),
		DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
}

// call runs a tool and returns its structured result as JSON, or the tool's
// error text.
func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (jsontext.Value, bool) {
	t.Helper()
	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return jsontext.Value(fmt.Sprintf("%q", err.Error())), true
	}
	if res.IsError {
		var b strings.Builder
		for _, c := range res.Content {
			if text, ok := c.(*mcp.TextContent); ok {
				b.WriteString(text.Text)
			}
		}
		return jsontext.Value(fmt.Sprintf("%q", b.String())), true
	}
	out, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return jsontext.Value(out), false
}

// tools lists the session's tool names.
func tools(t *testing.T, s *mcp.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// TestAgentSocket is the read and write checks of the MCP live check.
func TestAgentSocket(t *testing.T) {
	env, readToken, writeToken, _, socket := setup(t)
	ctx := t.Context()

	// A read token: the four read tools, and no write tool.
	read, err := connect(ctx, env, readToken)
	if err != nil {
		t.Fatalf("connect with the read token: %v", err)
	}
	defer func() { _ = read.Close() }()
	readTools := tools(t, read)
	t.Logf("read scope tools: %v", readTools)
	for _, want := range []string{"list_sources", "list_unread", "search", "read_item"} {
		if !contains(readTools, want) {
			t.Fatalf("the read token has no %s", want)
		}
	}
	for _, no := range []string{"save_url", "mark_read", "star"} {
		if contains(readTools, no) {
			t.Fatalf("the read token lists %s", no)
		}
	}

	var sources struct {
		Sources []struct {
			ID     int64  `json:"id"`
			Title  string `json:"title"`
			Unread int    `json:"unread"`
		} `json:"sources"`
	}
	out, failed := call(t, read, "list_sources", nil)
	if failed {
		t.Fatalf("list_sources: %s", out)
	}
	if err := json.Unmarshal(out, &sources); err != nil || len(sources.Sources) != 3 {
		t.Fatalf("list_sources = %s, %v; want 3 sources", out, err)
	}
	t.Logf("sources: %d", len(sources.Sources))

	var unread struct {
		Items []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"items"`
	}
	out, failed = call(t, read, "list_unread", map[string]any{"limit": 50})
	if failed {
		t.Fatalf("list_unread: %s", out)
	}
	if err := json.Unmarshal(out, &unread); err != nil || len(unread.Items) == 0 {
		t.Fatalf("list_unread = %s, %v", out, err)
	}
	t.Logf("unread: %d", len(unread.Items))

	var found struct {
		Items []struct {
			ID      int64  `json:"id"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
		} `json:"items"`
	}
	out, failed = call(t, read, "search", map[string]any{"query": "heron"})
	if failed {
		t.Fatalf("search: %s", out)
	}
	if err := json.Unmarshal(out, &found); err != nil || len(found.Items) != 1 {
		t.Fatalf("search heron = %s, %v; want 1", out, err)
	}
	if !strings.Contains(found.Items[0].Title, "autumn") {
		t.Fatalf("search heron found %q, want the autumn walk", found.Items[0].Title)
	}
	t.Logf("search heron: %q", found.Items[0].Title)

	var article struct {
		ID   int64  `json:"id"`
		Text string `json:"text"`
	}
	out, failed = call(t, read, "read_item", map[string]any{"id": found.Items[0].ID})
	if failed {
		t.Fatalf("read_item: %s", out)
	}
	if err := json.Unmarshal(out, &article); err != nil || !strings.Contains(article.Text, "heron") {
		t.Fatalf("read_item = %s, %v", out, err)
	}
	t.Logf("read_item: %d characters", len(article.Text))

	// save_url is not offered; calling it is refused.
	if _, failed := call(t, read, "save_url", map[string]any{"url": "https://example.org/x"}); !failed {
		t.Fatal("save_url succeeded under a read token")
	}

	// A write token: save a development article, mark one read, star one.
	write, err := connect(ctx, env, writeToken)
	if err != nil {
		t.Fatalf("connect with the write token: %v", err)
	}
	defer func() { _ = write.Close() }()
	for _, want := range []string{"save_url", "mark_read", "star"} {
		if !contains(tools(t, write), want) {
			t.Fatalf("the write token does not list %s", want)
		}
	}

	before := counts(t, socket)
	t.Logf("counts before: %v", before.Views)

	var saved struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	out, failed = call(t, write, "save_url", map[string]any{"url": "http://127.0.0.1:8088/articles/4"})
	if failed {
		t.Fatalf("save_url: %s", out)
	}
	if err := json.Unmarshal(out, &saved); err != nil || saved.ID == 0 {
		t.Fatalf("save_url = %s, %v", out, err)
	}
	t.Logf("save_url: item %d", saved.ID)
	// The saved link is a new unread item, so the unread count is taken
	// again before the mark.
	mid := counts(t, socket)

	// Mark the first unread item read, and star another.
	if len(unread.Items) < 2 {
		t.Fatalf("only %d unread items", len(unread.Items))
	}
	target, star := unread.Items[0].ID, unread.Items[1].ID
	if _, failed := call(t, write, "mark_read", map[string]any{"ids": []int64{target}}); failed {
		t.Fatal("mark_read failed")
	}
	if _, failed := call(t, write, "star", map[string]any{"id": star, "starred": true}); failed {
		t.Fatal("star failed")
	}

	after := counts(t, socket)
	t.Logf("counts after: %v", after.Views)
	if after.Views["later"] != before.Views["later"]+1 {
		t.Fatalf("later = %d, want %d", after.Views["later"], before.Views["later"]+1)
	}
	if after.Views["unread"] != mid.Views["unread"]-1 {
		t.Fatalf("unread = %d, want %d", after.Views["unread"], mid.Views["unread"]-1)
	}
	if after.Views["all"] != before.Views["all"]+1 {
		t.Fatalf("all = %d, want %d", after.Views["all"], before.Views["all"]+1)
	}
	if after.Views["starred"] != before.Views["starred"]+1 {
		t.Fatalf("starred = %d, want %d", after.Views["starred"], before.Views["starred"]+1)
	}
}

// TestBadScopeIs403 proves a token whose scope is not read or read;write
// is refused at initialize, before any tool.
func TestBadScopeIs403(t *testing.T) {
	env, _, _, badToken, _ := setup(t)
	status, body := initialize(t, env, badToken)
	if status != http.StatusForbidden {
		t.Fatalf("initialize with a write scope = %d %q, want 403", status, strings.TrimSpace(body))
	}
	t.Logf("initialize with a write scope: %d %s", status, strings.TrimSpace(body))
}

// TestRevokedTokenFails runs after lodgectl token revoke has revoked the
// write token: it sets READ_LIVE_REVOKED=1, and the token must no longer
// open a session.
func TestRevokedTokenFails(t *testing.T) {
	if os.Getenv("READ_LIVE_REVOKED") != "1" {
		t.Skip("set READ_LIVE_REVOKED=1 after revoking the write token")
	}
	env, _, writeToken, _, _ := setup(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	s, err := connect(ctx, env, writeToken)
	if err == nil {
		_ = s.Close()
		t.Fatal("a revoked token opened an MCP session")
	}
	t.Logf("a revoked token fails: %.120s", err.Error())
}

// counts asks readd's private socket for the counts a browser session sees.
func counts(t *testing.T, socket string) api.Counts {
	t.Helper()
	c, _, err := api.Dial(t.Context(), socket, "live")
	if err != nil {
		t.Fatalf("dial readd's private socket: %v", err)
	}
	defer func() { _ = c.Close() }()
	got, err := c.Read().Counts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// initialize posts one initialize to the agent socket and returns its
// status and body, for a scope the app must refuse.
func initialize(t *testing.T, env lodgeapp.Env, token string) (int, string) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"read-live","version":"1"}}}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, lodgeapp.AgentURL("read"), bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := env.AgentClient(token).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	return res.StatusCode, string(out)
}

// contains reports whether names holds want.
func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
