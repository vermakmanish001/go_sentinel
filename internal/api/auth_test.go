package api

import (
	"strings"
	"testing"
)

func TestPasswordHashingRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash is not argon2id: %s", hash)
	}
	if strings.Contains(hash, "correct horse") {
		t.Fatal("the plaintext password appears in the hash")
	}
	if !VerifyPassword("correct horse battery staple", hash) {
		t.Error("correct password rejected")
	}
	if VerifyPassword("wrong password", hash) {
		t.Error("wrong password accepted")
	}
}

// Equal passwords must not produce equal hashes, or the database leaks which
// accounts share a password.
func TestHashesAreSalted(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Error("two hashes of the same password are identical; the salt is not random")
	}
	if !VerifyPassword("same", a) || !VerifyPassword("same", b) {
		t.Error("salted hashes failed to verify")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	for _, bad := range []string{
		"", "not-a-hash", "$argon2id$", "$bcrypt$v=19$m=1,t=1,p=1$aaaa$bbbb",
		"$argon2id$v=19$m=bad,t=3,p=4$aaaa$bbbb",
		"$argon2id$v=19$m=65536,t=3,p=4$!!!!$bbbb",
	} {
		if VerifyPassword("anything", bad) {
			t.Errorf("malformed hash %q was accepted", bad)
		}
	}
}

// Sessions are looked up by hash, so a stolen database yields no usable cookie.
func TestTokensAreStoredHashed(t *testing.T) {
	token, err := newToken()
	if err != nil {
		t.Fatalf("newToken: %v", err)
	}
	if len(token) < 32 {
		t.Errorf("token is only %d chars; too little entropy", len(token))
	}

	h := hashToken(token)
	if h == token || strings.Contains(h, token) {
		t.Fatal("stored value reveals the token")
	}
	if h != hashToken(token) {
		t.Error("hashing is not deterministic")
	}

	other, _ := newToken()
	if token == other {
		t.Error("two generated tokens are identical")
	}
}
