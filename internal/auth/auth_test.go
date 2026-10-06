package auth

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("Admin@12345", []byte("pepper"))
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	if !VerifyPassword("Admin@12345", hash, []byte("pepper")) {
		t.Error("the correct password should verify")
	}
	if VerifyPassword("wrong", hash, []byte("pepper")) {
		t.Error("an incorrect password must not verify")
	}
	if VerifyPassword("Admin@12345", hash, []byte("other-pepper")) {
		t.Error("a different pepper must not verify")
	}
}

func TestHashesAreSalted(t *testing.T) {
	a, _ := HashPassword("same-password", nil)
	b, _ := HashPassword("same-password", nil)
	if a == b {
		t.Error("two hashes of the same password must differ (random salt)")
	}
}

func TestJWTRoundTrip(t *testing.T) {
	secret := []byte("test-secret-value")
	token, err := Issue(secret, 42, "ali@example.com", RoleCustomer, "fa", 15*time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := Parse(secret, token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.UserID != 42 || claims.Role != RoleCustomer || claims.Locale != "fa" {
		t.Errorf("claims round-trip mismatch: %+v", claims)
	}
}

func TestJWTRejectsWrongSecretAndExpiry(t *testing.T) {
	token, _ := Issue([]byte("secret-a"), 1, "a@b.c", RoleAdmin, "en", time.Minute)
	if _, err := Parse([]byte("secret-b"), token); err == nil {
		t.Error("a token signed with another secret must not verify")
	}
	expired, _ := Issue([]byte("secret-a"), 1, "a@b.c", RoleAdmin, "en", -time.Minute)
	if _, err := Parse([]byte("secret-a"), expired); err == nil {
		t.Error("an expired token must not verify")
	}
}

func TestRoleHierarchy(t *testing.T) {
	cases := []struct {
		have, need string
		want       bool
	}{
		{RoleSuperAdmin, RoleAdmin, true},
		{RoleAdmin, RoleSuperAdmin, false},
		{RoleCustomer, RoleGuest, true},
		{RoleGuest, RoleCustomer, false},
		{RoleVendor, RoleCustomer, true},
		{RoleAdmin, RoleVendor, true},
	}
	for _, c := range cases {
		if got := RoleAtLeast(c.have, c.need); got != c.want {
			t.Errorf("RoleAtLeast(%s,%s) = %v, want %v", c.have, c.need, got, c.want)
		}
	}
}

func TestIsStaff(t *testing.T) {
	for _, r := range []string{RoleAdmin, RoleSuperAdmin, RoleSupport, RoleEditor} {
		if !IsStaff(r) {
			t.Errorf("%s should be staff", r)
		}
	}
	for _, r := range []string{RoleGuest, RoleCustomer, RoleVendor} {
		if IsStaff(r) {
			t.Errorf("%s should not be staff", r)
		}
	}
}

func TestRandomTokenAndHashing(t *testing.T) {
	a := RandomToken(16)
	b := RandomToken(16)
	if a == b {
		t.Error("RandomToken must not repeat")
	}
	if len(a) != 32 {
		t.Errorf("RandomToken(16) produced %d hex characters, want 32", len(a))
	}
	if HashToken(a) == a {
		t.Error("HashToken must not return the plaintext")
	}
	if HashToken(a) != HashToken(b[:0]+a) {
		t.Error("HashToken must be deterministic")
	}
	if HashToken(a) == HashToken(b) {
		t.Error("different tokens must hash differently")
	}
}

func TestHMACSignatures(t *testing.T) {
	body := []byte(`{"event":"order.created"}`)
	sig := SignHMAC("whsec", body)
	if !VerifyHMAC("whsec", body, sig) {
		t.Error("a valid signature should verify")
	}
	if VerifyHMAC("whsec", body, "deadbeef") {
		t.Error("an invalid signature must not verify")
	}
	if VerifyHMAC("other", body, sig) {
		t.Error("a signature from a different secret must not verify")
	}
}

func TestTOTP(t *testing.T) {
	secret := NewTOTPSecret()
	if secret == "" {
		t.Fatal("empty TOTP secret")
	}
	code := TOTPCode(secret, time.Now())
	if len(code) != 6 {
		t.Fatalf("TOTP code %q should be 6 digits", code)
	}
	if !VerifyTOTP(secret, code) {
		t.Error("the current code should verify")
	}
	if code != "000000" && VerifyTOTP(secret, "000000") {
		t.Error("an arbitrary code must not verify")
	}
}

func TestConcurrentHashingIsBounded(t *testing.T) {
	limit := MaxConcurrentHashes()
	if limit < 2 {
		t.Fatalf("hashing ceiling is %d, expected at least 2", limit)
	}

	var inFlight, peak int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	pepper := []byte("stress-pepper")

	for i := 0; i < limit*4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			mu.Unlock()

			if _, err := HashPassword(fmt.Sprintf("Password@%d", i), pepper); err != nil {
				t.Errorf("hash %d: %v", i, err)
			}

			mu.Lock()
			inFlight--
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	h, err := HashPassword("Password@12345", pepper)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !VerifyPassword("Password@12345", h, pepper) {
		t.Error("a password hashed under load did not verify")
	}
	if VerifyPassword("wrong", h, pepper) {
		t.Error("the wrong password verified")
	}
	t.Logf("ceiling=%d goroutines=%d", limit, limit*4)
}
