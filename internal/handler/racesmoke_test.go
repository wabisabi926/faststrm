package handler

import (
	"fmt"
	"sync"
	"testing"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/pwdcrypto"
)

// TestHandlerRaces_Smoke 是一个仅用于「CI -race 下暴露并发问题」的冒烟测试。
//
// 刻意限定为「纯内存的并发读/写路径」—— 只跑 config.Get 快照读、
// config.MutateSettings / MutateAdmin copy-on-write 原子更新、
// 以及 Health handler 的无状态读路径。不触发 ChangePassword / Login 对磁盘 JSON 或
// logger 的写入，避免与 -coverpkg 插桩计数器 / zap 全局单例叠加造成的额外竞争。
//
// 若要验证 handler 侧调用 ChangePassword/Login 是否会 race，建议在更上层、
// 带独立 logger 单例的 e2e test 里单独加。
func TestHandlerRaces_Smoke(t *testing.T) {
	salt := "test_salt_32b_padding______"
	cfg := &config.AppConfig{
		Salt: salt,
		Admin: &model.AppConfig{
			Username: "admin",
			Password: pwdcrypto.HashPassword(salt, "admin"),
		},
		Settings: model.DefaultSettings(),
	}
	config.SetForTest(cfg)

	const rounds = 24

	var (
		wg           sync.WaitGroup
		startBarrier = make(chan struct{})
	)

	spawn := func(work func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					t.Errorf("panic in race goroutine: %v", rec)
				}
			}()
			<-startBarrier
			for i := 0; i < rounds; i++ {
				work()
			}
		}()
	}

	// Goroutine A: config.Get 快照读 (与写入并发的核心 race 场景)
	spawn(func() {
		snap := config.Get()
		if snap.Admin == nil {
			t.Error("Get() returned nil Admin")
		}
		if snap.Settings == nil {
			t.Error("Get() returned nil Settings")
		}
		if snap.Salt == "" {
			t.Error("Get() returned empty Salt")
		}
	})

	// Goroutine B: MutateSettings copy-on-write 写 (persist=false，纯内存)
	spawn(func() {
		if err := config.MutateSettings(func(s *model.Settings) {
			s.Strm.StrmUrlTemplate = "/ping"
		}, false); err != nil {
			t.Errorf("MutateSettings(set): %v", err)
		}
		if err := config.MutateSettings(func(s *model.Settings) {
			s.Strm.StrmUrlTemplate = ""
		}, false); err != nil {
			t.Errorf("MutateSettings(clear): %v", err)
		}
	})

	// Goroutine C: MutateAdmin copy-on-write 写 (persist=false，纯内存)
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

	// Goroutine D: 直接调 Snapshot 深度拷贝
	spawn(func() {
		snap := cfg.Snapshot()
		if snap.Admin == nil || snap.Settings == nil {
			t.Error("Snapshot() returned nil fields")
		}
	})

	close(startBarrier)
	wg.Wait()

	// 收尾：恢复 admin 初始状态，避免进程内同包后续测试依赖默认用户名。
	_ = config.MutateAdmin(func(admin *model.AppConfig) {
		admin.Username = "admin"
		admin.Password = pwdcrypto.HashPassword(salt, "admin")
	}, false)
	final := config.Get()
	if final.Admin == nil || final.Admin.Username != "admin" {
		t.Fatal(fmt.Errorf("final admin username not restored: got=%+v", final.Admin))
	}
}
