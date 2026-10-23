package store

import (
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

func TestAccountStorePersistsCookieStatus(t *testing.T) {
	dir := t.TempDir()
	s, err := NewAccountStore("test_salt", dir)
	if err != nil {
		t.Fatal(err)
	}
	acc := &model.AccountInfo{Name: "alice", AccountType: "115", Cookie: "UID=1;CID=2;SEID=3;KID=4"}
	if err := s.Upsert(acc); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCookieStatus("alice", model.CookieStatusInvalid, 99, model.CookieSourceMonitor); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewAccountStore("test_salt", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()

	got := reloaded.Get("alice")
	if got == nil {
		t.Fatal("account not reloaded")
	}
	if got.EffectiveCookieStatus() != model.CookieStatusInvalid {
		t.Fatalf("cookie status lost after restart: %q", got.EffectiveCookieStatus())
	}
	if got.CookieErrno != 99 || got.CookieSource != model.CookieSourceMonitor {
		t.Fatalf("cookie metadata lost: errno=%d source=%q", got.CookieErrno, got.CookieSource)
	}
	if got.LastCookieCheck == 0 {
		t.Fatal("lastCookieCheck lost after restart")
	}
}
