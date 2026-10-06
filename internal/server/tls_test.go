package server

import (
	"context"
	"testing"
	"time"

	"tund/internal/protocol"
)

func TestDecideAllowsTunnelOnOtherNode(t *testing.T) {
	s := &Server{cfg: &Config{BaseDomain: "tund.example", DashboardHost: "tund.example"}, reg: NewRegistry()}
	s.cluster = &cluster{
		s: s, name: "hel-1",
		nodes:  map[string]nodeInfo{"fsn-1": {Name: "fsn-1", LastSeen: time.Now()}},
		owners: map[string]owner{"app.customer.example": {nodes: []string{"fsn-1"}, proto: protocol.ProtoHTTP, expires: time.Now().Add(time.Minute)}},
	}
	c := &Certs{srv: s}
	if err := c.decide(context.Background(), "app.customer.example"); err != nil {
		t.Fatalf("a hostname served by another node must get a certificate here too: %v", err)
	}
}
