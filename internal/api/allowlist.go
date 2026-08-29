package api

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Allowlist decides which hosts a load test may target.
//
// This is the control that separates a load-testing tool from an open traffic
// cannon. Without it, anyone who can reach the API can aim the whole fleet at
// any host on the internet — or at internal services the API can route to but
// the caller cannot.
type Allowlist struct {
	// patterns are host globs: "api.example.com", "*.example.com", "localhost".
	// Empty means unrestricted, which is logged loudly at startup.
	patterns []string
}

// blockedHosts are refused unless a pattern names them explicitly. The cloud
// metadata address is the classic SSRF target: reachable from inside most
// deployments and holding credentials.
var blockedHosts = map[string]string{
	"169.254.169.254":          "cloud instance metadata",
	"metadata.google.internal": "cloud instance metadata",
}

func NewAllowlist(patterns []string) *Allowlist {
	cleaned := make([]string, 0, len(patterns))
	for _, p := range patterns {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return &Allowlist{patterns: cleaned}
}

// Unrestricted reports whether any host is permitted.
func (a *Allowlist) Unrestricted() bool { return len(a.patterns) == 0 }

// Check validates a plan's base URL. The error is written for the person who
// submitted the plan, so it names what was rejected and what is permitted.
func (a *Allowlist) Check(baseURL string) error {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return fmt.Errorf("could not parse base_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("base_url must be http or https, got %q", u.Scheme)
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("base_url has no host")
	}

	// An explicit pattern wins, including over the blocklist: someone testing
	// their own metadata service should be able to say so.
	for _, p := range a.patterns {
		if matchHost(p, host) {
			return nil
		}
	}

	if reason, blocked := blockedHosts[host]; blocked {
		return fmt.Errorf("target %q is blocked (%s)", host, reason)
	}

	if a.Unrestricted() {
		return nil
	}

	return fmt.Errorf("target %q is not in the allowed list (%s)",
		host, strings.Join(a.patterns, ", "))
}

// matchHost supports an exact host or a single leading "*." wildcard. The
// wildcard covers subdomains only — "*.example.com" does not match
// "example.com" itself, and never matches across a dot boundary.
func matchHost(pattern, host string) bool {
	if pattern == host {
		return true
	}
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		return strings.HasSuffix(host, "."+suffix) && host != suffix
	}
	// A bare IP pattern also matches its canonical form (e.g. zero-padded).
	if ip := net.ParseIP(pattern); ip != nil {
		if hostIP := net.ParseIP(host); hostIP != nil {
			return ip.Equal(hostIP)
		}
	}
	return false
}
