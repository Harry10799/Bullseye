// Package tlsconfig loads TLS certificates for the hub's gRPC and WebSocket servers.
//
// In production, mount certs at /etc/bullseye/tls/{tls.crt,tls.key} (e.g., via
// cert-manager). The hub auto-detects them and enables TLS.
//
// When no certs are found (dev mode), servers run plaintext — log a warning.
package tlsconfig

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
)

// Config holds TLS configuration.
type Config struct {
	CertFile string
	KeyFile  string
	CAFile   string // optional — for client cert verification (mTLS)
}

// Load loads a tls.Config from the given paths. Returns nil if cert/key files
// don't exist (dev mode — caller should fall back to plaintext).
func Load(cfg Config) (*tls.Config, error) {
	if cfg.CertFile == "" || cfg.KeyFile == "" {
		log.Warn().Msg("tls: no cert/key configured — running plaintext (dev mode)")
		return nil, nil
	}

	if _, err := os.Stat(cfg.CertFile); err != nil {
		log.Warn().Err(err).Str("cert", cfg.CertFile).Msg("tls: cert file not found — running plaintext")
		return nil, nil
	}
	if _, err := os.Stat(cfg.KeyFile); err != nil {
		log.Warn().Err(err).Str("key", cfg.KeyFile).Msg("tls: key file not found — running plaintext")
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("tls: load key pair: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		},
	}

	if cfg.CAFile != "" {
		caPool := x509.NewCertPool()
		caBytes, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("tls: read CA: %w", err)
		}
		if !caPool.AppendCertsFromPEM(caBytes) {
			return nil, fmt.Errorf("tls: failed to parse CA certs from %s", cfg.CAFile)
		}
		tlsCfg.ClientCAs = caPool
		tlsCfg.ClientAuth = tls.VerifyClientCertIfGiven
	}

	log.Info().
		Str("cert", cfg.CertFile).
		Str("key", cfg.KeyFile).
		Bool("mTLS", cfg.CAFile != "").
		Msg("tls: certificates loaded")

	return tlsCfg, nil
}

// AutoLoad reads TLS config from standard env vars.
//
//	TLS_CERT_FILE=/etc/bullseye/tls/tls.crt
//	TLS_KEY_FILE=/etc/bullseye/tls/tls.key
//	TLS_CA_FILE=/etc/bullseye/tls/ca.crt   (optional, enables mTLS)
func AutoLoad() (*tls.Config, error) {
	return Load(Config{
		CertFile: os.Getenv("TLS_CERT_FILE"),
		KeyFile:  os.Getenv("TLS_KEY_FILE"),
		CAFile:   os.Getenv("TLS_CA_FILE"),
	})
}

// Enabled returns true if TLS is configured.
func Enabled(cfg *tls.Config) bool {
	return cfg != nil
}

// LogTLSState logs the current TLS configuration summary.
func LogTLSState(cfg *tls.Config) {
	if !Enabled(cfg) {
		border := strings.Repeat("=", 60)
		log.Warn().Msg(border + "\n" +
			"WARNING: TLS is NOT enabled. The hub is running in plaintext mode.\n" +
			"Captured traffic data (including redacted-but-sensitive content)\n" +
			"traverses the network in plaintext. DO NOT deploy this to production.\n" +
			"To enable TLS, set TLS_CERT_FILE and TLS_KEY_FILE env vars.\n" +
			border)
		return
	}
	log.Info().Msg("tls: enabled (production-ready)")
}
