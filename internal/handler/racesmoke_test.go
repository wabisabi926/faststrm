package handler

import (
	"sync"
	"testing"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/pwdcrypto"
)

// TestHandlerRaces_Smoke 是一个专门用于「-race 下抓并发竞争」的冒烟测试。
//
// 本测试**不做任何断言**：只要没有触发 -race detector，测试即视为通过。
// 给 CI 的 race detector 提供高并发的 config 读写样本，覆盖
// config.Get() 快照读 / MutateSettings / MutateAdmin copy-on-write 三条路径。
func TestHandlerRaces_Smoke(t *testing.T) {
	salt := "test_salt_32b_padding______"
	config.SetForTest(&config.AppConfig{
		Salt: salt,
		Admin: &model.AppConfig{
			Username: "admin",
			Password: pwdcrypto.HashPassword(salt, "admin"),
		},
		Settings: model.DefaultSettings(),
	})

	const rounds = 50

	var wg sync.WaitGroup
	startBarrier := make(chan struct{})

	spawn := func(work func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startBarrier
			for i := 0; i < rounds; i++ {
				work()
			}
		}()
	}

	// 并发路径 A: config.Get 快照读
	spawn(func() {
		snap := config.Get()
		_ = snap.Salt
		if snap.Admin != nil {
			_ = snap.Admin.Username
		}
		if snap.Settings != nil {
			_ = snap.Settings.Strm.StrmUrlTemplate
		}
	})

	// 并发路径 B: MutateSettings copy-on-write 写 (persist=false, 零磁盘 I/O)
	spawn(func() {
		_ = config.MutateSettings(func(s *model.Settings) {
			s.Strm.StrmUrlTemplate = "/ping"
		}, false)
		_ = config.MutateSettings(func(s *model.Settings) {
			s.Strm.StrmUrlTemplate = ""
		}, false)
	})

	// 并发路径 C: MutateAdmin copy-on-write 写 (persist=false, 零磁盘 I/O)
	spawn(func() {
		_ = config.MutateAdmin(func(admin *model.AppConfig) {
			admin.Username = "swapped"
			admin.Password = pwdcrypto.HashPassword(salt, "swapped")
		}, false)
		_ = config.MutateAdmin(func(admin *model.AppConfig) {
			admin.Username = "admin"
			admin.Password = pwdcrypto.HashPassword(salt, "admin")
		}, false)
	})

	close(startBarrier)
	wg.Wait()
}
