package tundmcp

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// drainTimeout bounds how long stdin EOF is held back for unanswered requests.
const drainTimeout = 30 * time.Second

// DrainingStdio is mcp.StdioTransport, except that when stdin reaches EOF it
// waits until every request read so far has been answered before passing the
// EOF on. Without this the SDK ends the session immediately and scripted use
// (`printf '…' | tund mcp`) loses its responses.
func DrainingStdio() mcp.Transport {
	return drainingTransport(os.Stdin, os.Stdout, drainTimeout)
}

func drainingTransport(in io.ReadCloser, out io.Writer, timeout time.Duration) mcp.Transport {
	p := &pendingIDs{ids: map[string]bool{}}
	return &mcp.IOTransport{
		Reader: &drainReader{r: bufio.NewReader(in), c: in, p: p, timeout: timeout},
		Writer: &trackWriter{w: out, p: p},
	}
}

type pendingIDs struct {
	mu  sync.Mutex
	ids map[string]bool
}

// frame is the part of a JSON-RPC message needed to pair requests with responses.
type frame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

func (p *pendingIDs) observe(line []byte, incoming bool) {
	var f frame
	if json.Unmarshal(line, &f) != nil || len(f.ID) == 0 || string(f.ID) == "null" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case incoming && f.Method != "":
		p.ids[string(f.ID)] = true // a request we must answer
	case !incoming && f.Method == "":
		delete(p.ids, string(f.ID)) // our response to it
	}
}

func (p *pendingIDs) empty() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.ids) == 0
}

type drainReader struct {
	r       *bufio.Reader
	c       io.Closer
	p       *pendingIDs
	timeout time.Duration
	buf     []byte
	eof     bool
}

func (d *drainReader) Read(b []byte) (int, error) {
	for len(d.buf) == 0 {
		if d.eof {
			deadline := time.Now().Add(d.timeout)
			for !d.p.empty() && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			return 0, io.EOF
		}
		line, err := d.r.ReadBytes('\n')
		if len(line) > 0 {
			d.p.observe(line, true)
			d.buf = line
		}
		if err != nil {
			if err != io.EOF {
				return 0, err
			}
			d.eof = true
		}
	}
	n := copy(b, d.buf)
	d.buf = d.buf[n:]
	return n, nil
}

func (d *drainReader) Close() error { return d.c.Close() }

type trackWriter struct {
	mu   sync.Mutex
	w    io.Writer
	p    *pendingIDs
	line []byte
}

func (t *trackWriter) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.w.Write(b)
	// Track only after the bytes are out, so a drained EOF never races the
	// response it waited for.
	t.line = append(t.line, b[:n]...)
	for {
		i := indexNewline(t.line)
		if i < 0 {
			break
		}
		t.p.observe(t.line[:i], false)
		t.line = t.line[i+1:]
	}
	return n, err
}

func (t *trackWriter) Close() error { return nil }

func indexNewline(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}
