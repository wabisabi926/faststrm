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
