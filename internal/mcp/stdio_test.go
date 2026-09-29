package tundmcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// safeBuffer is an io.Writer that can be read while the server writes.
type safeBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Requests piped in with an immediate EOF must still be answered.
func TestDrainingStdioAnswersBeforeEOF(t *testing.T) {
	in := io.NopCloser(strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"slow","arguments":{}}}`,
	}, "\n") + "\n"))
	out := &safeBuffer{}

	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "slow"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		time.Sleep(200 * time.Millisecond)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Run(ctx, drainingTransport(in, out, 5*time.Second))

	got := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var f struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal([]byte(line), &f) == nil && f.Result != nil {
			got[string(f.ID)] = true
		}
	}
	if !got["1"] || !got["2"] {
		t.Fatalf("missing responses, output:\n%s", out.String())
	}
}
