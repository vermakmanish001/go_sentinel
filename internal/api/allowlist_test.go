package api

import "testing"

func TestAllowlistUnrestrictedStillBlocksMetadata(t *testing.T) {
	a := NewAllowlist(nil)

	if !a.Unrestricted() {
		t.Fatal("empty allowlist should be unrestricted")
	}
	if err := a.Check("https://anything.example.com/api"); err != nil {
		t.Errorf("unrestricted list rejected a normal host: %v", err)
	}
	// Unrestricted must not mean "including the credentials endpoint".
	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://metadata.google.internal/computeMetadata/v1/",
	} {
		if err := a.Check(target); err == nil {
			t.Errorf("%s was allowed; cloud metadata must be blocked by default", target)
		}
	}
}

func TestAllowlistMatching(t *testing.T) {
	a := NewAllowlist([]string{"api.example.com", "*.staging.example.com", "localhost"})

	allowed := []string{
		"https://api.example.com/v1",
		"https://a.staging.example.com",
		"https://deep.nested.staging.example.com",
		"http://localhost:8080/get",
		"http://API.EXAMPLE.COM/v1", // host comparison is case-insensitive
	}
	for _, u := range allowed {
		if err := a.Check(u); err != nil {
			t.Errorf("%s should be allowed: %v", u, err)
		}
	}

	denied := []string{
		"https://example.com",         // parent of a wildcard is not covered
		"https://staging.example.com", // the wildcard is subdomains only
		"https://evil.com",
		"https://api.example.com.evil.com", // suffix-confusion must not pass
		"https://notlocalhost",
	}
	for _, u := range denied {
		if err := a.Check(u); err == nil {
			t.Errorf("%s should be denied", u)
		}
	}
}

func TestAllowlistRejectsNonHTTPSchemes(t *testing.T) {
	a := NewAllowlist(nil)
	for _, u := range []string{"file:///etc/passwd", "gopher://x", "ftp://host/x"} {
		if err := a.Check(u); err == nil {
			t.Errorf("%s should be rejected: only http and https are testable", u)
		}
	}
	if err := a.Check("not a url at all"); err == nil {
		t.Error("a base_url with no host should be rejected")
	}
}

// An explicit pattern is an operator decision and outranks the default block.
func TestExplicitPatternOverridesBlocklist(t *testing.T) {
	a := NewAllowlist([]string{"169.254.169.254"})
	if err := a.Check("http://169.254.169.254/latest"); err != nil {
		t.Errorf("explicitly allowed host was still blocked: %v", err)
	}
}
