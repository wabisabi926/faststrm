package auth

import (
	"strings"
	"testing"
	"time"
)

func TestJWTSignParse(t *testing.T) {
	iss := NewTokenIssuer([]byte("test-secret-test-secret-32bytes!!"))
	iss.Validity = 1 * time.Hour

	tok, err := iss.Sign("admin")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !strings.HasPrefix(tok, "eyJ") {
		t.Fatalf("token prefix invalid: %s", tok[:10])
	}
	claims, err := iss.Parse(tok)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if claims.Username != "admin" {
		t.Fatalf("username got %s", claims.Username)
	}
	if claims.Issuer != "faststrm-go" {
		t.Fatalf("issuer wrong: %s", claims.Issuer)
	}
}

func TestJWTInvalidSignature(t *testing.T) {
	iss1 := NewTokenIssuer([]byte("secret-a"))
	iss2 := NewTokenIssuer([]byte("secret-b"))
	tok, _ := iss1.Sign("u")
	if _, err := iss2.Parse(tok); err == nil {
		t.Fatalf("different secret should fail")
	}
}

func TestJWTExpired(t *testing.T) {
	iss := NewTokenIssuer([]byte("s"))
	iss.Validity = -1 * time.Minute // 过期
	tok, _ := iss.Sign("u")
	if _, err := iss.Parse(tok); err == nil {
		t.Fatalf("expired token should fail")
	}
}

func TestJWTRevocationPersists(t *testing.T) {
	secret := []byte("test-secret-test-secret-32bytes!!")
	path := t.TempDir() + "/revocations.json"
	iss := NewTokenIssuer(secret)
	iss.Revocations = NewRevocationList(path)

	tok, err := iss.Sign("admin")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := iss.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	if claims.ID == "" {
		t.Fatal("token must carry a jti")
	}
	iss.Revoke(claims.ID, claims.ExpiresAt.Time)

	restarted := NewTokenIssuer(secret)
	restarted.Revocations = NewRevocationList(path)
	if _, err := restarted.Parse(tok); err == nil {
		t.Fatal("revoked token should stay revoked after restart")
	}
}

func TestJWTRevocationOnlyAffectsOneToken(t *testing.T) {
	iss := NewTokenIssuer([]byte("test-secret-test-secret-32bytes!!"))
	iss.Revocations = NewRevocationList("")
	tokA, _ := iss.Sign("admin")
	tokB, _ := iss.Sign("admin")
	claimsA, err := iss.Parse(tokA)
	if err != nil {
		t.Fatal(err)
	}
	iss.Revoke(claimsA.ID, claimsA.ExpiresAt.Time)
	if _, err := iss.Parse(tokA); err == nil {
		t.Fatal("token A should be revoked")
	}
	if _, err := iss.Parse(tokB); err != nil {
		t.Fatalf("token B should remain valid: %v", err)
	}
}

func TestGenerateSecret(t *testing.T) {
	s, err := GenerateSecret()
	if err != nil || len(s) != 64 {
		t.Fatalf("GenerateSecret: len=%d err=%v", len(s), err)
	}
	s2, _ := GenerateSecret()
	if s == s2 {
		t.Fatalf("two secrets identical!")
	}
}
