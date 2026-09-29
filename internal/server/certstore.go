package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgCertStorage is a certmagic.Storage in Postgres so every node shares
// certificates, the ACME account and in-flight challenges.
type pgCertStorage struct {
	pool  *pgxpool.Pool
	owner string // lock owner id of this process

	mu     sync.Mutex
	renews map[string]context.CancelFunc
}

const certLockTTL = 2 * time.Minute

var _ certmagic.Storage = (*pgCertStorage)(nil)

func newPGCertStorage(pool *pgxpool.Pool, owner string) *pgCertStorage {
	return &pgCertStorage{pool: pool, owner: owner, renews: map[string]context.CancelFunc{}}
}

func (s *pgCertStorage) Store(ctx context.Context, key string, value []byte) error {
	_, err := s.pool.Exec(ctx, `insert into certmagic_data (key, value, modified) values ($1, $2, now())
		on conflict (key) do update set value = excluded.value, modified = now()`, key, value)
	return err
}

func (s *pgCertStorage) Load(ctx context.Context, key string) ([]byte, error) {
	var v []byte
	err := s.pool.QueryRow(ctx, `select value from certmagic_data where key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fs.ErrNotExist
	}
	return v, err
}

func (s *pgCertStorage) Delete(ctx context.Context, key string) error {
	_, err := s.pool.Exec(ctx, `delete from certmagic_data where key = $1 or starts_with(key, $1 || '/')`, key)
	return err
}

func (s *pgCertStorage) Exists(ctx context.Context, key string) bool {
	var ok bool
	err := s.pool.QueryRow(ctx, `select exists(select 1 from certmagic_data where key = $1 or starts_with(key, $1 || '/'))`, key).Scan(&ok)
	return err == nil && ok
}

func (s *pgCertStorage) List(ctx context.Context, path string, recursive bool) ([]string, error) {
	prefix := strings.TrimSuffix(path, "/") + "/"
	rows, err := s.pool.Query(ctx, `select key from certmagic_data where starts_with(key, $1)`, prefix)
	if err != nil {
		return nil, err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fs.ErrNotExist
	}
	if recursive {
		sort.Strings(keys)
		return keys, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, k := range keys {
		child, _, _ := strings.Cut(strings.TrimPrefix(k, prefix), "/")
		if full := prefix + child; !seen[full] {
			seen[full] = true
			out = append(out, full)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *pgCertStorage) Stat(ctx context.Context, key string) (certmagic.KeyInfo, error) {
	var size int64
	var modified time.Time
	err := s.pool.QueryRow(ctx, `select length(value), modified from certmagic_data where key = $1`, key).Scan(&size, &modified)
	if err == nil {
		return certmagic.KeyInfo{Key: key, Modified: modified, Size: size, IsTerminal: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return certmagic.KeyInfo{}, err
	}
	if s.Exists(ctx, key) {
		return certmagic.KeyInfo{Key: key, IsTerminal: false}, nil
	}
	return certmagic.KeyInfo{}, fs.ErrNotExist
}

// Lock takes a named lock with a lease that is renewed while held; a lease
// that runs out (crashed node) can be taken over.
func (s *pgCertStorage) Lock(ctx context.Context, name string) error {
	for {
		var got string
		err := s.pool.QueryRow(ctx, `insert into certmagic_locks (name, owner, expires_at) values ($1, $2, now() + $3::interval)
			on conflict (name) do update set owner = excluded.owner, expires_at = excluded.expires_at
			where certmagic_locks.expires_at < now() or certmagic_locks.owner = excluded.owner
			returning name`, name, s.owner, fmt.Sprintf("%d seconds", int(certLockTTL.Seconds()))).Scan(&got)
		if err == nil {
			s.startRenew(name)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (s *pgCertStorage) startRenew(name string) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if old := s.renews[name]; old != nil {
		old()
	}
	s.renews[name] = cancel
	s.mu.Unlock()
	go func() {
		t := time.NewTicker(certLockTTL / 3)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.pool.Exec(ctx, `update certmagic_locks set expires_at = now() + $3::interval where name = $1 and owner = $2`,
					name, s.owner, fmt.Sprintf("%d seconds", int(certLockTTL.Seconds())))
			}
		}
	}()
}

func (s *pgCertStorage) Unlock(ctx context.Context, name string) error {
	s.mu.Lock()
	if cancel := s.renews[name]; cancel != nil {
		cancel()
		delete(s.renews, name)
	}
	s.mu.Unlock()
	_, err := s.pool.Exec(ctx, `delete from certmagic_locks where name = $1 and owner = $2`, name, s.owner)
	return err
}

// importCertFiles copies an existing file-based certmagic storage into
// Postgres once (the table is empty), keeping the ACME account and certs.
func (s *pgCertStorage) importCertFiles(ctx context.Context, dir string) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `select count(*) from certmagic_data`).Scan(&n); err != nil || n > 0 {
		return 0, err
	}
	if _, err := os.Stat(dir); err != nil {
		return 0, nil
	}
	imported := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if strings.HasPrefix(key, "locks/") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := s.Store(ctx, key, b); err != nil {
			return err
		}
		imported++
		return nil
	})
	return imported, err
}
