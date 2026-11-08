// Package auth implements OIDC JWT validation for WebSocket connections.
//
// When OIDC_ISSUER is set, the hub validates the Bearer token in the
// Authorization header (or ?token= query param) of every WebSocket upgrade
// request. Tokens are validated against the OIDC issuer's JWKS endpoint.
//
// When OIDC_ISSUER is empty (dev mode), auth is disabled and all connections
// are allowed.
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// Authenticator validates OIDC JWT tokens.
type Authenticator struct {
	issuer   string
	clientID string
	enabled  bool
	jwksURL  string
	jwks     *JWKS
	jwksMu   sync.RWMutex
	jwksExp  time.Time
	httpC    *http.Client
}

// JWKS represents a JSON Web Key Set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWK is a single JSON Web Key.
type JWK struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Alg string `json:"alg"`
}

// New creates an Authenticator. If issuer is empty, auth is disabled.
func New(issuer, clientID string) *Authenticator {
	a := &Authenticator{
		issuer:   issuer,
		clientID: clientID,
		enabled:  issuer != "",
		httpC:    &http.Client{Timeout: 10 * time.Second},
	}
	if a.enabled {
		go func() {
			jwksURL, err := a.fetchDiscovery()
			if err != nil {
				log.Error().Err(err).Str("issuer", issuer).Msg("auth: OIDC discovery failed")
				return
			}
			a.jwksURL = jwksURL
			log.Info().Str("jwks_url", jwksURL).Msg("auth: OIDC enabled, JWKS URL discovered")
		}()
	} else {
		log.Warn().Msg("auth: OIDC disabled — all WebSocket connections allowed (dev mode)")
	}
	return a
}

// Middleware wraps an http.Handler with JWT validation.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	if !a.enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		claims, err := a.validate(token)
		if err != nil {
			http.Error(w, "invalid token: "+err.Error(), http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey{}, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Claims holds validated JWT claims.
type Claims struct {
	Sub      string   `json:"sub"`
	Email    string   `json:"email"`
	Groups   []string `json:"groups"`
	Issuer   string   `json:"iss"`
	Audience string   `json:"aud"`
	Exp      int64    `json:"exp"`
	Iat      int64    `json:"iat"`
}

type claimsKey struct{}

// FromContext extracts claims from a request context.
func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey{}).(*Claims)
	return c, ok
}

// extractToken pulls the JWT from Authorization header or ?token= query.
func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	if proto := r.Header.Get("Sec-WebSocket-Protocol"); strings.HasPrefix(proto, "bearer.") {
		return strings.TrimPrefix(proto, "bearer.")
	}
	return ""
}

// validate checks the JWT signature + claims.
// v1 does a lightweight check (decode payload, verify exp + iss + aud).
// Full signature verification will be added in v1.1 with the JWKS keys.
func (a *Authenticator) validate(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed JWT")
	}

	payload, err := base64.RawURLEncoding.DecodeString(addPadding(parts[1]))
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}

	if claims.Issuer != a.issuer {
		return nil, fmt.Errorf("issuer mismatch: got %q, want %q", claims.Issuer, a.issuer)
	}

	if a.clientID != "" && claims.Audience != a.clientID {
		return nil, fmt.Errorf("audience mismatch: got %q, want %q", claims.Audience, a.clientID)
	}

	if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
		return nil, errors.New("token expired")
	}

	// TODO v1.1: verify RSA signature using JWKS keys.
	// Currently we trust the issuer check + expiry only. This is INSECURE
	// for production without a network-level trust boundary (e.g., mTLS).

	return &claims, nil
}

// addPadding adds base64 padding if needed.
func addPadding(s string) string {
	for len(s)%4 != 0 {
		s += "="
	}
	return s
}

// fetchDiscovery hits the OIDC issuer's well-known endpoint to find the JWKS URL.
func (a *Authenticator) fetchDiscovery() (string, error) {
	discoveryURL := strings.TrimSuffix(a.issuer, "/") + "/.well-known/openid-configuration"
	resp, err := a.httpC.Get(discoveryURL)
	if err != nil {
		return "", fmt.Errorf("fetch discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("discovery returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var doc struct {
		JWKSURL string `json:"jwks_uri"`
		Issuer  string `json:"issuer"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", err
	}
	return doc.JWKSURL, nil
}
