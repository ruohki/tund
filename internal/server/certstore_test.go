package server

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"
)

// Runs against a real database: TUND_TEST_DATABASE_URL=postgres://… go test ./internal/server -run CertStore
func TestCertStorePostgres(t *testing.T) {
	dsn := os.Getenv("TUND_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TUND_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := OpenStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	st.pool.Exec(ctx, `delete from certmagic_data where key like 'test/%'`)
	a, b := newPGCertStorage(st.pool, "node-a"), newPGCertStorage(st.pool, "node-b")

	if _, err := a.Load(ctx, "test/missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing key: %v", err)
	}
	for _, k := range []string{"test/certs/x/x.crt", "test/certs/x/x.key", "test/certs/y/y.crt", "test/acct.json"} {
		if err := a.Store(ctx, k, []byte("v:"+k)); err != nil {
			t.Fatal(err)
		}
	}
	if v, err := b.Load(ctx, "test/certs/x/x.crt"); err != nil || string(v) != "v:test/certs/x/x.crt" {
		t.Fatalf("shared load: %q %v", v, err)
	}
	if l, _ := a.List(ctx, "test/certs", false); len(l) != 2 || l[0] != "test/certs/x" {
		t.Fatalf("list: %v", l)
	}
	if l, _ := a.List(ctx, "test/certs", true); len(l) != 3 {
		t.Fatalf("recursive list: %v", l)
	}
	if ki, err := a.Stat(ctx, "test/certs/x"); err != nil || ki.IsTerminal {
		t.Fatalf("stat dir: %+v %v", ki, err)
	}
	if !a.Exists(ctx, "test/certs") || a.Exists(ctx, "test/nope") {
		t.Fatal("exists")
	}
	if err := a.Delete(ctx, "test/certs/x"); err != nil || a.Exists(ctx, "test/certs/x/x.key") {
		t.Fatalf("delete dir: %v", err)
	}

	// Locks: b waits until a unlocks.
	if err := a.Lock(ctx, "test-lock"); err != nil {
		t.Fatal(err)
	}
	got := make(chan time.Time, 1)
	go func() {
		b.Lock(ctx, "test-lock")
		got <- time.Now()
	}()
	time.Sleep(1500 * time.Millisecond)
	unlocked := time.Now()
	a.Unlock(ctx, "test-lock")
	select {
	case at := <-got:
		if at.Before(unlocked) {
			t.Fatal("b acquired the lock while a held it")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("b never got the lock")
	}
	b.Unlock(ctx, "test-lock")
	st.pool.Exec(ctx, `delete from certmagic_data where key like 'test/%'`)
}
