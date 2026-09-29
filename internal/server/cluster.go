package server

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"time"

	"tund/internal/protocol"
)

// Edge nodes (docs/SPEC.md "Edge nodes"): nodes share the database, find the
// node that holds a tunnel and relay visitor traffic there over an
// authenticated TLS link.

const (
	nodeHeartbeat = 10 * time.Second
	nodeDeadAfter = 45 * time.Second
	ownerCacheTTL = 5 * time.Second
	relayMaxSkew  = 60 * time.Second

	hdrRelayRemote = "X-Tund-Relay-Remote"
)

type nodeInfo struct {
	Name, Role, Region, RelayURL, CertSHA, Version string
	LastSeen                                       time.Time
}

func (n nodeInfo) alive() bool { return time.Since(n.LastSeen) < nodeDeadAfter }

type owner struct {
	node    string
	proto   string
	expires time.Time
}

type cluster struct {
	s       *Server
	name    string
	cert    tls.Certificate
	certSHA string

	mu      sync.Mutex
	nodes   map[string]nodeInfo
	owners  map[string]owner // hostname or "tcp:<port>" -> owning node
	nonces  map[string]time.Time
	proxies map[string]*httputil.ReverseProxy // per node

	remoteTCP map[int]net.Listener // listeners for TCP tunnels on other nodes

	httpConns chan net.Conn // authenticated "http" relay connections
}

func newCluster(s *Server) (*cluster, error) {
	cert, sha, err := selfSignedRelayCert(s.cfg.NodeName())
	if err != nil {
		return nil, err
	}
	return &cluster{
		s: s, name: s.cfg.NodeName(), cert: cert, certSHA: sha,
		nodes: map[string]nodeInfo{}, owners: map[string]owner{}, nonces: map[string]time.Time{},
		proxies: map[string]*httputil.ReverseProxy{}, remoteTCP: map[int]net.Listener{},
		httpConns: make(chan net.Conn),
	}, nil
}

func selfSignedRelayCert(name string) (tls.Certificate, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "tund relay " + name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	sum := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, hex.EncodeToString(sum[:]), nil
}

// run keeps this node registered, tracks the other nodes and TCP tunnels,
// and serves the relay listener.
func (c *cluster) run(ctx context.Context) error {
	if err := c.heartbeat(ctx); err != nil {
		return err
	}
	ln, err := tls.Listen("tcp", c.s.cfg.RelayAddr, &tls.Config{Certificates: []tls.Certificate{c.cert}, MinVersion: tls.VersionTLS13})
	if err != nil {
		return fmt.Errorf("relay listener: %w", err)
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go c.acceptRelay(ln)
	go c.serveRelayHTTP(ctx)
	go c.s.store.Listen(ctx, "tund_tunnels", c.onTunnelEvent)
	go func() {
		t := time.NewTicker(nodeHeartbeat)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := c.heartbeat(ctx); err != nil && ctx.Err() == nil {
					logf("node heartbeat: %v", err)
				}
			}
		}
	}()
	logf("cluster: node %s (%s, %s) relay on %s, reachable at %s", c.name, c.s.cfg.Role, c.s.cfg.NodeRegion, c.s.cfg.RelayAddr, c.s.cfg.RelayURL)
	return nil
}

func (c *cluster) heartbeat(ctx context.Context) error {
	st := c.s.store
	if err := st.UpsertNode(ctx, nodeInfo{Name: c.name, Role: c.s.cfg.Role, Region: c.s.cfg.NodeRegion,
		RelayURL: c.s.cfg.RelayURL, CertSHA: c.certSHA, Version: Version}); err != nil {
		return err
	}
	nodes, err := st.Nodes(ctx)
	if err != nil {
		return err
	}
	m := map[string]nodeInfo{}
	for _, n := range nodes {
		m[n.Name] = n
	}
	c.mu.Lock()
	c.nodes = m
	c.mu.Unlock()
	if err := st.EndDeadNodes(ctx, nodeDeadAfter); err != nil {
		logf("end tunnels of dead nodes: %v", err)
	}
	c.syncRemoteTCP(ctx)
	return nil
}

func (c *cluster) node(name string) (nodeInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.nodes[name]
	return n, ok && n.alive()
}

// controlNode picks a live control node for dashboard traffic.
func (c *cluster) controlNode() (nodeInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range c.nodes {
		if n.Role == "control" && n.alive() && n.Name != c.name {
			return n, true
		}
	}
	return nodeInfo{}, false
}

// ownerOf returns the live node (other than this one) holding a tunnel for
// key: a hostname, or "tcp:<port>".
func (c *cluster) ownerOf(ctx context.Context, key string) (nodeInfo, string, bool) {
	c.mu.Lock()
	o, ok := c.owners[key]
	c.mu.Unlock()
	if !ok || time.Now().After(o.expires) {
		node, proto, err := c.s.store.TunnelNode(ctx, key)
		if err != nil && !errors.Is(err, errNotFound) {
			logf("owner of %s: %v", key, err)
		}
		o = owner{node: node, proto: proto, expires: time.Now().Add(ownerCacheTTL)}
		c.mu.Lock()
		c.owners[key] = o
		c.mu.Unlock()
	}
	if o.node == "" || o.node == c.name {
		return nodeInfo{}, "", false
	}
	n, alive := c.node(o.node)
	return n, o.proto, alive
}

// onTunnelEvent invalidates the owner cache and tracks remote TCP tunnels.
func (c *cluster) onTunnelEvent(payload string) {
	if payload == "" {
		return
	}
	var ev struct {
		Hostname   string `json:"hostname"`
		Node       string `json:"node"`
		Proto      string `json:"proto"`
		RemotePort int    `json:"remote_port"`
	}
	if json.Unmarshal([]byte(payload), &ev) != nil {
		return
	}
	c.mu.Lock()
	delete(c.owners, ev.Hostname)
	if ev.RemotePort > 0 {
		delete(c.owners, "tcp:"+strconv.Itoa(ev.RemotePort))
	}
	c.mu.Unlock()
	if ev.Proto == protocol.ProtoTCP && ev.Node != c.name {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c.syncRemoteTCP(ctx)
	}
}

// --- relay frames ---

type relayFrame struct {
	Kind   string    `json:"kind"` // http | raw | cmd
	TS     int64     `json:"ts"`
	Nonce  string    `json:"nonce"`
	MAC    string    `json:"mac"`
	Proto  string    `json:"proto,omitempty"`  // raw: tcp | tls
	Target string    `json:"target,omitempty"` // raw: port or hostname
	Remote string    `json:"remote,omitempty"` // raw: visitor address
	Cmd    *relayCmd `json:"cmd,omitempty"`
}

type relayCmd struct {
	Cmd       string          `json:"cmd"` // stop | takeover | replay | ping
	TunnelID  string          `json:"tunnel_id,omitempty"`
	UserID    string          `json:"user_id,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	Override  *replayOverride `json:"override,omitempty"`
}

type relayReply struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Status    int    `json:"status,omitempty"`
}

func (c *cluster) mac(f *relayFrame) string {
	m := hmac.New(sha256.New, []byte(c.s.cfg.Secret))
	fmt.Fprintf(m, "relay\x00%s\x00%d\x00%s\x00%s\x00%s\x00%s", f.Kind, f.TS, f.Nonce, f.Proto, f.Target, f.Remote)
	if f.Cmd != nil {
		b, _ := json.Marshal(f.Cmd)
		m.Write(b)
	}
	return hex.EncodeToString(m.Sum(nil))
}

func writeRelayFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > 1<<20 {
		return errors.New("relay frame too large")
	}
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(b)))
	_, err = w.Write(append(l[:], b...))
	return err
}

func readRelayFrame(r io.Reader, v any) error {
	var l [4]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(l[:])
	if n > 1<<20 {
		return errors.New("relay frame too large")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// dial opens an authenticated relay connection to node n.
func (c *cluster) dial(ctx context.Context, n nodeInfo, f relayFrame) (net.Conn, error) {
	d := &tls.Dialer{Config: &tls.Config{
		InsecureSkipVerify: true, // the peer is pinned below
		MinVersion:         tls.VersionTLS13,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("relay: no certificate")
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if !hmac.Equal([]byte(hex.EncodeToString(sum[:])), []byte(n.CertSHA)) {
				return fmt.Errorf("relay: certificate of node %s does not match its published fingerprint", n.Name)
			}
			return nil
		},
	}}
	conn, err := d.DialContext(ctx, "tcp", n.RelayURL)
	if err != nil {
		return nil, fmt.Errorf("relay to node %s: %w", n.Name, err)
	}
	f.TS = time.Now().Unix()
	f.Nonce = randomToken(12)
	f.MAC = c.mac(&f)
	if err := writeRelayFrame(conn, f); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (c *cluster) acceptRelay(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go c.handleRelay(conn)
	}
}

func (c *cluster) handleRelay(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var f relayFrame
	if err := readRelayFrame(conn, &f); err != nil {
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{})
	if !c.verify(&f) {
		logf("relay: rejected %s frame from %s", f.Kind, conn.RemoteAddr())
		conn.Close()
		return
	}
	switch f.Kind {
	case "http":
		c.httpConns <- conn
	case "raw":
		c.handleRawRelay(conn, f)
	case "cmd":
		defer conn.Close()
		reply := c.s.runRelayCmd(f.Cmd)
		writeRelayFrame(conn, reply)
	default:
		conn.Close()
	}
}

func (c *cluster) verify(f *relayFrame) bool {
	if d := time.Since(time.Unix(f.TS, 0)); d > relayMaxSkew || d < -relayMaxSkew {
		return false
	}
	if !hmac.Equal([]byte(c.mac(f)), []byte(f.MAC)) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, exp := range c.nonces {
		if now.After(exp) {
			delete(c.nonces, k)
		}
	}
	if _, seen := c.nonces[f.Nonce]; seen {
		return false
	}
	c.nonces[f.Nonce] = now.Add(2 * relayMaxSkew)
	return true
}

// --- HTTP relaying ---

type relayedKey struct{}

// isRelayed reports whether r arrived over the relay (never relay it again).
func isRelayed(r *http.Request) bool { return r.Context().Value(relayedKey{}) != nil }

// serveRelayHTTP serves authenticated "http" relay connections as visitor
// traffic for this node's tunnels.
func (c *cluster) serveRelayHTTP(ctx context.Context) {
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if remote := r.Header.Get(hdrRelayRemote); remote != "" {
				r.RemoteAddr = remote
			}
			r.Header.Del(hdrRelayRemote)
			c.s.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), relayedKey{}, true)))
		}),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          nil,
	}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	srv.Serve(&chanListener{ch: c.httpConns, done: ctx.Done()})
}

type chanListener struct {
	ch   chan net.Conn
	done <-chan struct{}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *chanListener) Close() error   { return nil }
func (l *chanListener) Addr() net.Addr { return &net.TCPAddr{} }

// relayHTTP proxies a visitor request to the node that holds the tunnel (or
// to a control node for dashboard traffic).
func (c *cluster) relayHTTP(w http.ResponseWriter, r *http.Request, n nodeInfo) {
	c.mu.Lock()
	p := c.proxies[n.Name]
	if p == nil || c.nodes[n.Name].CertSHA != n.CertSHA {
		p = c.newRelayProxy(n)
		c.proxies[n.Name] = p
	}
	c.mu.Unlock()
	p.ServeHTTP(w, r)
}

func (c *cluster) newRelayProxy(n nodeInfo) *httputil.ReverseProxy {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return c.dial(ctx, n, relayFrame{Kind: "http"})
		},
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     60 * time.Second,
		DisableCompression:  true,
	}
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "node-" + n.Name
			pr.Out.Host = pr.In.Host
			// The receiving node trusts this header only on relay connections;
			// never pass one through from a visitor.
			pr.Out.Header.Del(hdrRelayRemote)
			pr.Out.Header.Set(hdrRelayRemote, pr.In.RemoteAddr)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) {
				return
			}
			logf("relay to node %s: %v", n.Name, err)
			c.s.renderPage(w, r, http.StatusBadGateway, pageMessage, map[string]any{
				"Title": "Tunnel temporarily unreachable", "Message": "The server holding this tunnel did not answer. Try again in a moment.",
			})
		},
	}
}

// --- raw (TCP/TLS) relaying ---

// relayRaw pipes a visitor connection (plus already-read bytes) to node n.
func (c *cluster) relayRaw(n nodeInfo, proto, target string, visitor net.Conn, prefix []byte) {
	defer visitor.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	conn, err := c.dial(ctx, n, relayFrame{Kind: "raw", Proto: proto, Target: target, Remote: visitor.RemoteAddr().String()})
	cancel()
	if err != nil {
		logf("%v", err)
		return
	}
	defer conn.Close()
	if len(prefix) > 0 {
		if _, err := conn.Write(prefix); err != nil {
			return
		}
	}
	done := make(chan struct{}, 2)
	go func() { io.Copy(conn, visitor); closeWrite(conn); done <- struct{}{} }()
	go func() { io.Copy(visitor, conn); closeWrite(visitor); done <- struct{}{} }()
	<-done
	select {
	case <-done:
	case <-time.After(30 * time.Second):
	}
}

// remoteAddrConn overrides RemoteAddr with the visitor's address.
type remoteAddrConn struct {
	net.Conn
	remote net.Addr
}

func (c *remoteAddrConn) RemoteAddr() net.Addr { return c.remote }

func (c *remoteAddrConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Conn.Close()
}

type strAddr string

func (a strAddr) Network() string { return "tcp" }
func (a strAddr) String() string  { return string(a) }

func (c *cluster) handleRawRelay(conn net.Conn, f relayFrame) {
	var t *Tunnel
	switch f.Proto {
	case protocol.ProtoTCP:
		port, _ := strconv.Atoi(f.Target)
		if c.s.tcp != nil {
			c.s.tcp.mu.Lock()
			t = c.s.tcp.used[port]
			c.s.tcp.mu.Unlock()
		}
	case protocol.ProtoTLS:
		if cand := c.s.reg.Lookup(f.Target); cand != nil && cand.Proto == protocol.ProtoTLS {
			t = cand
		}
	}
	if t == nil {
		conn.Close()
		return
	}
	c.s.pipeConn(t, &remoteAddrConn{Conn: conn, remote: strAddr(f.Remote)}, nil)
}

// syncRemoteTCP listens on the ports of TCP tunnels held by other nodes so
// visitors can reach them through this node too.
func (c *cluster) syncRemoteTCP(ctx context.Context) {
	if c.s.tcp == nil {
		return
	}
	remote, err := c.s.store.RemoteTCPTunnels(ctx, c.name, nodeDeadAfter)
	if err != nil {
		logf("remote TCP tunnels: %v", err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for port, ln := range c.remoteTCP {
		if _, ok := remote[port]; !ok {
			ln.Close()
			delete(c.remoteTCP, port)
		}
	}
	for port := range remote {
		if _, ok := c.remoteTCP[port]; ok || !c.s.tcp.inRange(port) {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			continue
		}
		c.remoteTCP[port] = ln
		go func(port int, ln net.Listener) {
			for {
				visitor, err := ln.Accept()
				if err != nil {
					return
				}
				n, _, ok := c.ownerOf(context.Background(), "tcp:"+strconv.Itoa(port))
				if !ok {
					visitor.Close()
					continue
				}
				go c.relayRaw(n, protocol.ProtoTCP, strconv.Itoa(port), visitor, nil)
			}
		}(port, ln)
	}
}

// releaseRemoteTCP frees a port this node was proxying so a local tunnel can take it.
func (c *cluster) releaseRemoteTCP(port int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ln := c.remoteTCP[port]; ln != nil {
		ln.Close()
		delete(c.remoteTCP, port)
	}
}

// --- commands ---

func (c *cluster) command(ctx context.Context, n nodeInfo, cmd relayCmd) (*relayReply, error) {
	conn, err := c.dial(ctx, n, relayFrame{Kind: "cmd", Cmd: &cmd})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	var reply relayReply
	if err := readRelayFrame(bufio.NewReader(conn), &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// runRelayCmd executes a command for another node against local tunnels.
func (s *Server) runRelayCmd(cmd *relayCmd) relayReply {
	if cmd == nil {
		return relayReply{Error: "missing command"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	switch cmd.Cmd {
	case "ping":
		return relayReply{OK: true}
	case "stop":
		if t := s.reg.ByID(cmd.TunnelID); t != nil && (cmd.UserID == "" || t.UserID == cmd.UserID) {
			reason := cmd.Reason
			if reason == "" {
				reason = "stopped from the dashboard"
			}
			t.session.unbind(t.BindID, reason)
		}
		return relayReply{OK: true}
	case "takeover":
		// A client of the same account reconnected through another node.
		t := s.reg.ByID(cmd.TunnelID)
		if t == nil {
			s.store.EndTunnel(ctx, cmd.TunnelID)
			return relayReply{OK: true}
		}
		if t.UserID != cmd.UserID || !t.session.stale() {
			return relayReply{Error: "in use"}
		}
		t.session.close("replaced by a new connection")
		return relayReply{OK: true}
	case "replay":
		id, status, err := s.replay(ctx, cmd.UserID, cmd.RequestID, cmd.Override)
		if err != nil {
			return relayReply{Error: err.Error()}
		}
		return relayReply{OK: true, RequestID: id, Status: status}
	}
	return relayReply{Error: "unknown command " + cmd.Cmd}
}

// nodeStatus lists the cluster's nodes for the admin overview.
func (s *Server) nodeStatus(ctx context.Context) []map[string]any {
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return nil
	}
	counts, _ := s.store.TunnelsPerNode(ctx)
	out := []map[string]any{}
	for _, n := range nodes {
		out = append(out, map[string]any{"name": n.Name, "role": n.Role, "region": n.Region, "alive": n.alive(),
			"tunnels": counts[n.Name], "version": n.Version, "last_seen": n.LastSeen})
	}
	return out
}

// localDashboardPath reports whether the dashboard-host path must stay on this node.
func localDashboardPath(p string) bool {
	return strings.HasPrefix(p, "/_tund/") || p == "/install.sh" || p == "/install.ps1"
}
