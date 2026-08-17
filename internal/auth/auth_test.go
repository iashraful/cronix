package auth

import (
	"strings"
	"testing"
	"time"
)

func TestSessionTokenRoundTrip(t *testing.T) {
	token, err := IssueSessionToken([]byte("secret"), "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	user, ok := ValidateSessionToken([]byte("secret"), token)
	if !ok || user != "admin" {
		t.Fatalf("validate: ok=%v user=%q", ok, user)
	}
}

func TestSessionTokenTamperedSignature(t *testing.T) {
	token, err := IssueSessionToken([]byte("secret"), "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	body, _, _ := strings.Cut(token, ".")
	tampered := body + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, ok := ValidateSessionToken([]byte("secret"), tampered); ok {
		t.Fatal("tampered signature must be rejected")
	}
}

func TestSessionTokenWrongKey(t *testing.T) {
	token, err := IssueSessionToken([]byte("secret"), "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, ok := ValidateSessionToken([]byte("other"), token); ok {
		t.Fatal("wrong key must be rejected")
	}
}

func TestSessionTokenExpired(t *testing.T) {
	token, err := IssueSessionToken([]byte("secret"), "admin", -time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, ok := ValidateSessionToken([]byte("secret"), token); ok {
		t.Fatal("expired token must be rejected")
	}
}

func TestSessionTokenMalformed(t *testing.T) {
	for _, token := range []string{"", "abc", "a.b.c", "..", ".sig", "body."} {
		if _, ok := ValidateSessionToken([]byte("secret"), token); ok {
			t.Errorf("malformed token %q must be rejected", token)
		}
	}
	if _, ok := ValidateSessionToken([]byte("secret"), "AAAA.!!!not-base64!!!"); ok {
		t.Fatal("bad base64 signature must be rejected")
	}
}
