// Package redact implements header denylist + body regex redaction for PII
// and sensitive data flowing through the hub.
//
// Redaction is applied BEFORE persistence and BEFORE relaying to the UI,
// so sensitive data never leaves the hub process. The redaction rules
// are configurable via env vars (see config.Config).
package redact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

// Redactor applies header denylist + body regex rules to entries.
// It is safe for concurrent use.
type Redactor struct {
	headerSet map[string]struct{}

	// compiled body patterns — applied to JSON-pretty-printed bodies
	bodyPatterns []*regexp.Regexp

	// stats for observability
	mu       sync.Mutex
	stats    Stats
}

// Stats tracks redaction counts for the metrics endpoint.
type Stats struct {
	HeadersRedacted int
	BodiesRedacted  int
	BodiesScanned   int
}

// New creates a Redactor from config.
//
// headers: comma-separated list of header names (case-insensitive) to redact
// bodyPatterns: comma-separated list of regex patterns to redact in bodies
//
// Built-in patterns always active when RedactEnabled=true:
//   - JWT tokens: eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+
//   - Credit cards (basic): \b(?:\d[ -]*?){13,16}\b
//   - AWS keys: AKIA[0-9A-Z]{16}
//   - Bearer tokens: Bearer\s+[A-Za-z0-9_-]+
//   - Stripe tokens: (sk|pk|tok)_(test_)?[a-z0-9]{16,}
var builtinPatterns = []string{
	`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`, // JWT
	`\b(?:\d[ -]*?){13,16}\b`,                                     // credit card
	`AKIA[0-9A-Z]{16}`,                                            // AWS access key
	`(?i)bearer\s+[A-Za-z0-9_\-\.=]+`,                             // Bearer token
	`(?i)(sk|pk|tok)_(test_)?[a-z0-9]{16,}`,                       // Stripe
	`(?i)password["'\s:=]+\S+`,                                    // password=...
	`(?i)(api[_-]?key|secret)["'\s:=]+\S+`,                        // api_key=...
}

func New(headers, bodyPatterns string) (*Redactor, error) {
	r := &Redactor{
		headerSet: make(map[string]struct{}),
	}

	for _, h := range strings.Split(headers, ",") {
		h = strings.TrimSpace(strings.ToLower(h))
		if h != "" {
			r.headerSet[h] = struct{}{}
		}
	}

	// always include builtins
	allPatterns := append([]string{}, builtinPatterns...)
	for _, p := range strings.Split(bodyPatterns, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			allPatterns = append(allPatterns, p)
		}
	}

	for _, p := range allPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("redact: invalid regex %q: %w", p, err)
		}
		r.bodyPatterns = append(r.bodyPatterns, re)
	}

	log.Info().
		Strs("headers", headerKeys(r.headerSet)).
		Int("body_patterns", len(r.bodyPatterns)).
		Msg("redactor initialized")

	return r, nil
}

// RedactHeaders redacts sensitive headers in-place. Returns the count of
// redacted header values.
func (r *Redactor) RedactHeaders(headers map[string]string) int {
	count := 0
	for k := range headers {
		lk := strings.ToLower(k)
		if _, deny := r.headerSet[lk]; deny {
			headers[k] = "[REDACTED]"
			count++
		}
		// also run body patterns against header values — they may contain
		// JWTs or credit cards even in non-denylisted headers
		for _, re := range r.bodyPatterns {
			if re.MatchString(headers[k]) {
				headers[k] = re.ReplaceAllString(headers[k], "[REDACTED]")
				count++
			}
		}
	}
	if count > 0 {
		r.mu.Lock()
		r.stats.HeadersRedacted += count
		r.mu.Unlock()
	}
	return count
}

// RedactBody applies all redaction patterns to a body. If the body looks like
// JSON, it's pretty-printed first so patterns can match field values reliably.
// Returns the redacted body and whether any redaction was applied.
func (r *Redactor) RedactBody(body []byte) ([]byte, bool) {
	if len(body) == 0 {
		return body, false
	}

	r.mu.Lock()
	r.stats.BodiesScanned++
	r.mu.Unlock()

	// try to pretty-print JSON for better pattern matching
	pretty, isJSON := tryPrettyJSON(body)
	work := body
	if isJSON {
		work = pretty
	}

	redacted := false
	for _, re := range r.bodyPatterns {
		if re.Match(work) {
			work = re.ReplaceAll(work, []byte("[REDACTED]"))
			redacted = true
		}
	}

	if redacted {
		r.mu.Lock()
		r.stats.BodiesRedacted++
		r.mu.Unlock()
	}

	// if we pretty-printed, minify back to save bytes on the wire
	if isJSON && redacted {
		if minified, err := jsonCompact(work); err == nil {
			return minified, true
		}
	}

	return work, redacted
}

// Stats returns a snapshot of redaction counts.
func (r *Redactor) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

// --- helpers ---

func tryPrettyJSON(body []byte) ([]byte, bool) {
	var v interface{}
	if err := json.Unmarshal(body, &v); err != nil {
		return body, false
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return body, false
	}
	return pretty, true
}

func jsonCompact(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, body); err != nil {
		return body, err
	}
	return buf.Bytes(), nil
}

func headerKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	return keys
}
