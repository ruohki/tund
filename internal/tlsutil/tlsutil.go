// Package tlsutil builds the TLS configuration the client uses to reach a
// tund server, honouring TUND_CA_FILE for servers with a private CA.
package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
)

var (
	once   sync.Once
	config *tls.Config
	cfgErr error
)

// ClientConfig returns nil (use the system roots) unless TUND_CA_FILE points
// to a PEM bundle, whose certificates are then trusted in addition.
func ClientConfig() (*tls.Config, error) {
	once.Do(func() {
		path := os.Getenv("TUND_CA_FILE")
		if path == "" {
			return
		}
		pem, err := os.ReadFile(path)
		if err != nil {
			cfgErr = fmt.Errorf("TUND_CA_FILE: %w", err)
			return
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			cfgErr = fmt.Errorf("TUND_CA_FILE: no PEM certificates in %s", path)
			return
		}
		config = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	})
	return config, cfgErr
}

// MustClientConfig is ClientConfig for places that cannot return errors; a
// broken TUND_CA_FILE is reported on stderr and ignored.
func MustClientConfig() *tls.Config {
	c, err := ClientConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tund:", err)
	}
	return c
}
