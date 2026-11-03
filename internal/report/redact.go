// Package report builds, redacts, and exports BeeChief's portable Honey Report
// diagnostic documents (issue #11).
package report

import (
	"regexp"
	"strings"
)

// RedactionToken replaces sensitive values in Honey Reports.
const RedactionToken = "[REDACTED]"

var (
	// Private keys (PEM blocks).
	pemBlockRE = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	// Authorization / Bearer headers.
	authHeaderRE = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer|basic|token)\s+)[^\s"',;]+`)
	bearerRE     = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)
	// Common API token prefixes (OpenAI/OpenRouter sk-, GitHub ghp_/gho_/github_pat_, Slack xoxb-/xoxp-, Google AIza, Anthropic sk-ant-).
	knownTokenRE = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{16,}|xox[baprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{20,})\b`)
	// Key=value or YAML/JSON fields holding secrets, passwords, tokens, or API keys.
	secretKVPairRE = regexp.MustCompile(`(?i)\b(password|passwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token|private[_-]?key|client[_-]?secret)\b(\s*[:=]\s*["']?)([^"'\s,;}{]+)`)
	// URL user:password@host credentials.
	urlCredRE = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]+:)([^@\s/]+)(@)`)
)

// Redact scrubs obvious secrets, tokens, authorization headers, passwords, URL
// credentials, and private keys from text before it leaves BeeChief.
func Redact(input string) string {
	if input == "" {
		return ""
	}
	out := pemBlockRE.ReplaceAllString(input, "-----BEGIN PRIVATE KEY-----\n"+RedactionToken+"\n-----END PRIVATE KEY-----")
	out = authHeaderRE.ReplaceAllString(out, "${1}"+RedactionToken)
	out = bearerRE.ReplaceAllString(out, "${1}"+RedactionToken)
	out = knownTokenRE.ReplaceAllString(out, RedactionToken)
	out = secretKVPairRE.ReplaceAllString(out, "${1}${2}"+RedactionToken)
	out = urlCredRE.ReplaceAllString(out, "${1}"+RedactionToken+"${3}")
	// Also strip any data:image/... base64 blobs so avatars don't bloat reports.
	if strings.Contains(out, "data:image/") {
		out = dataURIRE.ReplaceAllString(out, "data:image/[REDACTED]")
	}
	return out
}

var dataURIRE = regexp.MustCompile(`data:image/[a-zA-Z0-9.+-]+;base64,[A-Za-z0-9+/=]+`)
