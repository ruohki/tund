package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tund/internal/protocol"
	"tund/internal/pwhash"
)

// Policy is the effective access policy of a live tunnel.
type Policy struct {
	Mode         string // protocol.AuthNone / AuthPassword / AuthOIDC
	PasswordHash string
	// PasswordTag identifies a password given by the client (keyed hash):
	// unlike the salted PasswordHash it is the same on every bind, so visitor
	// cookies survive restarts and work across the members of a pool.
	PasswordTag string
	ProviderID  string
	Allow       []string
}

// Fingerprint changes whenever the policy changes, which invalidates visitor cookies.
func (p Policy) Fingerprint() string {
	pw := p.PasswordHash
	if p.PasswordTag != "" {
		pw = p.PasswordTag
	}
	h := sha256.New()
	h.Write([]byte(p.Mode + "\x00" + pw + "\x00" + p.ProviderID + "\x00" + strings.Join(p.Allow, ",")))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// EmailAllowed checks an email against the allow list.
func (p Policy) EmailAllowed(email string) bool { return p.Allowed(email, nil) }

// Allowed checks a signed-in identity against the allow list: "a@b.com",
// "@b.com" / "b.com" (email domain) or "group:<name>" (exact group); an empty
// list admits everyone.
func (p Policy) Allowed(email string, groups []string) bool {
	if len(p.Allow) == 0 {
		return true
	}
	email = strings.ToLower(strings.TrimSpace(email))
	for _, a := range p.Allow {
		a = strings.TrimSpace(a)
		if len(a) > 6 && strings.EqualFold(a[:6], "group:") {
			if slices.Contains(groups, strings.TrimSpace(a[6:])) {
				return true
			}
			continue
		}
		a = strings.ToLower(a)
		if email == "" && a != "" && a != "*" {
			continue
		}
		switch {
		case a == "" || a == "*":
			return true
		case strings.HasPrefix(a, "@"):
			if strings.HasSuffix(email, a) {
				return true
			}
		case !strings.Contains(a, "@"):
			if strings.HasSuffix(email, "@"+a) {
				return true
			}
		case a == email:
			return true
		}
	}
	return false
}

// Tunnel is a hostname routed to a local address through an agent session.
type Tunnel struct {
	ID           string // database id
	BindID       string // client-chosen id on the control stream
	Name         string
	Proto        string // protocol.ProtoHTTP, ProtoTCP or ProtoTLS
	RemotePort   int    // tcp: public port
	Hostname     string
	PublicURL    string
	LocalAddr    string
	HostHeader   string
	UserID       string
	Static       bool // hostname is one of the account's static hostnames
	StartedAt    time.Time
	TeamID       string         // team owning the hostname, if bound through team membership
	listener     net.Listener   // tcp: the public listener
	meter        *accountMeter  // bandwidth meter of the owning account
	allow        []netip.Prefix // IP allow list; empty = everyone
	warn         atomic.Bool    // browser visitors get the warning page (see warning.go)
	ownerTrusted atomic.Bool    // owner is an admin or trusted: never warn
	session      *AgentSession
	expiresAt    time.Time    // maximum lifetime at bind time; zero = unlimited
	expiryNoted  atomic.Bool  // the client was told the tunnel closes soon
	rules        *tunnelRules // http: traffic rules, nil = none
	// pool: shares the hostname with the account's other pool tunnels whose
	// poolKey (visitor-facing settings) is the same; visitors are spread
	// across them.
	pool     bool
	poolKey  string
	poolSize int // members on all nodes when this one was bound

	transport *http.Transport
	proxy     *httputil.ReverseProxy

	mu           sync.RWMutex
	policy       Policy
	clientPolicy *Policy  // what the client asked for (nil = nothing); overrides the domain unless its team requires single sign-on
	pwOK         sync.Map // sha256(password)+fingerprint -> struct{}, avoids re-running scrypt
}

func (t *Tunnel) Policy() Policy {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.policy
}

func (t *Tunnel) setPolicy(p Policy) {
	t.mu.Lock()
	t.policy = p
	t.mu.Unlock()
	t.pwOK.Clear()
}

func (t *Tunnel) checkPassword(pw string) bool {
	p := t.Policy()
	if p.Mode != protocol.AuthPassword || pw == "" {
		return false
	}
	key := sha256Hex(pw) + p.Fingerprint()
	if _, ok := t.pwOK.Load(key); ok {
		return true
	}
	if pwhash.Verify(pw, p.PasswordHash) {
		t.pwOK.Store(key, struct{}{})
		return true
	}
	return false
}

// Registry holds all live agent sessions and tunnels.
type Registry struct {
	mu       sync.RWMutex
	byHost   map[string]*hostSlot // routable tunnels
	pending  map[string][]*Tunnel // claimed hostnames still being set up
	byID     map[string]*Tunnel
	sessions map[string]*AgentSession
}

// hostSlot holds the routable tunnels of a hostname: one, or the members of
// a load-balanced pool (copy on write, picked round-robin).
type hostSlot struct {
	members []*Tunnel
	next    atomic.Uint64
}

func NewRegistry() *Registry {
	return &Registry{byHost: map[string]*hostSlot{}, pending: map[string][]*Tunnel{}, byID: map[string]*Tunnel{}, sessions: map[string]*AgentSession{}}
}

// Lookup returns the tunnel for host; for a pool, the next member in turn.
func (r *Registry) Lookup(host string) *Tunnel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	slot := r.byHost[host]
	if slot == nil {
		return nil
	}
	if len(slot.members) == 1 {
		return slot.members[0]
	}
	return slot.members[(slot.next.Add(1)-1)%uint64(len(slot.members))]
}

// Members returns the routable tunnels of host.
func (r *Registry) Members(host string) []*Tunnel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if slot := r.byHost[host]; slot != nil {
		return slot.members
	}
	return nil
}

func (r *Registry) ByID(id string) *Tunnel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byID[id]
}

// joins reports whether t may join cur's hostname as another pool member.
func joins(t, cur *Tunnel) bool {
	return t.pool && cur.pool && t.UserID == cur.UserID && t.poolKey == cur.poolKey
}

// Claim reserves host for t; it returns a current holder if host is taken.
// Pool members with matching settings share the hostname.
func (r *Registry) Claim(t *Tunnel) (existing *Tunnel, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var holders []*Tunnel
	if slot := r.byHost[t.Hostname]; slot != nil {
		holders = slot.members
	}
	holders = append(slices.Clip(holders), r.pending[t.Hostname]...)
	for _, cur := range holders {
		if !joins(t, cur) {
			return cur, false
		}
	}
	r.pending[t.Hostname] = append(r.pending[t.Hostname], t)
	return nil, true
}

// Activate makes a claimed, fully set up tunnel routable.
func (r *Registry) Activate(t *Tunnel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pend := r.pending[t.Hostname]
	i := slices.Index(pend, t)
	if i < 0 {
		return
	}
	r.setPending(t.Hostname, slices.Delete(slices.Clone(pend), i, i+1))
	slot := r.byHost[t.Hostname]
	members := []*Tunnel{t}
	if slot != nil {
		members = append(slices.Clone(slot.members), t)
	}
	next := &hostSlot{members: members}
	if slot != nil {
		next.next.Store(slot.next.Load())
	}
	r.byHost[t.Hostname] = next
	r.byID[t.ID] = t
}

func (r *Registry) setPending(host string, ts []*Tunnel) {
	if len(ts) == 0 {
		delete(r.pending, host)
	} else {
		r.pending[host] = ts
	}
}

func (r *Registry) Release(t *Tunnel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if slot := r.byHost[t.Hostname]; slot != nil {
		if i := slices.Index(slot.members, t); i >= 0 {
			if len(slot.members) == 1 {
				delete(r.byHost, t.Hostname)
			} else {
				next := &hostSlot{members: slices.Delete(slices.Clone(slot.members), i, i+1)}
				next.next.Store(slot.next.Load())
				r.byHost[t.Hostname] = next
			}
		}
	}
	if pend := r.pending[t.Hostname]; slices.Contains(pend, t) {
		i := slices.Index(pend, t)
		r.setPending(t.Hostname, slices.Delete(slices.Clone(pend), i, i+1))
	}
	if t.ID != "" && r.byID[t.ID] == t {
		delete(r.byID, t.ID)
	}
}

// Tunnels returns every routable tunnel, pool members included.
func (r *Registry) Tunnels() []*Tunnel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Tunnel, 0, len(r.byHost))
	for _, slot := range r.byHost {
		out = append(out, slot.members...)
	}
	return out
}

// InUse reports whether host is routed or being set up.
func (r *Registry) InUse(host string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byHost[host] != nil || len(r.pending[host]) > 0
}

// CountForUser counts online and pending tunnels of a user.
func (r *Registry) CountForUser(userID string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, slot := range r.byHost {
		for _, t := range slot.members {
			if t.UserID == userID {
				n++
			}
		}
	}
	for _, ts := range r.pending {
		for _, t := range ts {
			if t.UserID == userID {
				n++
			}
		}
	}
	return n
}

func (r *Registry) AddSession(s *AgentSession) {
	r.mu.Lock()
	r.sessions[s.ID] = s
	r.mu.Unlock()
}

func (r *Registry) RemoveSession(s *AgentSession) {
	r.mu.Lock()
	delete(r.sessions, s.ID)
	r.mu.Unlock()
}

func (r *Registry) Sessions() []*AgentSession {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*AgentSession, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	return out
}
