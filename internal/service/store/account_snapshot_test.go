package store

import (
	"sync"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
)

func testCookieValid(v bool) *bool { return &v }

func TestAccountStoreReturnsIsolatedSnapshots(t *testing.T) {
	s, err := NewAccountStore("test_salt", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	valid := true
	input := &model.AccountInfo{
		Name: "alice", AccountType: "115", Cookie: "UID=1",
		CookieStatus: model.CookieStatusValid, CookieValid: &valid,
	}
	if err := s.Upsert(input); err != nil {
		t.Fatal(err)
	}

	// Upsert 之后修改调用方对象，不能影响 Store。
	input.Cookie = "UID=changed"
	*input.CookieValid = false
	got := s.Get("alice")
	if got == nil || got.Cookie != "UID=1" || !*got.CookieValid {
		t.Fatalf("store must not retain caller object: %+v", got)
	}

	// Get 返回副本：修改副本（含 CookieValid 指针）不能影响 Store。
	got.Cookie = "UID=mutated"
	got.CookieStatus = model.CookieStatusInvalid
	*got.CookieValid = false
	fresh := s.Get("alice")
	if fresh.Cookie != "UID=1" || fresh.CookieStatus != model.CookieStatusValid || !*fresh.CookieValid {
		t.Fatalf("snapshot mutated internal state: %+v", fresh)
	}

	// List 同样返回独立副本。
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("list len = %d", len(list))
	}
	list[0].Cookie = "UID=list-mutated"
	*list[0].CookieValid = false
	fresh = s.Get("alice")
	if fresh.Cookie != "UID=1" || !*fresh.CookieValid {
		t.Fatalf("list snapshot mutated internal state: %+v", fresh)
	}

	// ReadAccounts / WriteAccounts 也不能共享 CookieValid 指针。
	accounts, err := s.ReadAccounts()
	if err != nil {
		t.Fatal(err)
	}
	*accounts[0].CookieValid = false
	if fresh = s.Get("alice"); !*fresh.CookieValid {
		t.Fatal("ReadAccounts shares CookieValid pointer with store")
	}
	writeInput := []model.AccountInfo{{
		Name: "bob", AccountType: "115", Cookie: "UID=2", CookieValid: testCookieValid(true),
	}}
	if err := s.WriteAccounts(writeInput); err != nil {
		t.Fatal(err)
	}
	*writeInput[0].CookieValid = false
	bob := s.Get("bob")
	if bob == nil || !*bob.CookieValid || bob.Cookie != "UID=2" {
		t.Fatalf("WriteAccounts shares state with caller: %+v", bob)
	}
}

func TestAccountStoreConcurrentReadWriteSnapshots(t *testing.T) {
	s, err := NewAccountStore("test_salt", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Upsert(&model.AccountInfo{
		Name: "alice", AccountType: "115", Cookie: "UID=1", CookieStatus: model.CookieStatusUnknown,
	}); err != nil {
		t.Fatal(err)
	}

	const iterations = 2000
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < iterations; n++ {
				readAccountSnapshot(s.Get("alice"))
				for _, acc := range s.List() {
					readAccountSnapshot(acc)
				}
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < iterations; n++ {
				status := model.CookieStatusValid
				if (n+i)%2 == 0 {
					status = model.CookieStatusInvalid
				}
				_ = s.SetCookieStatus("alice", status, 0, model.CookieSourceMonitor)
				_ = s.Update("alice", func(a *model.AccountInfo) {
					a.Cookie = "UID=1"
				})
			}
		}(i)
	}
	wg.Wait()
}

func readAccountSnapshot(acc *model.AccountInfo) {
	if acc == nil {
		return
	}
	_ = acc.EffectiveCookieStatus()
	_ = acc.Cookie
	_ = acc.CookieStatus
	if acc.CookieValid != nil {
		_ = *acc.CookieValid
	}
}
