package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/auth"
	"github.com/wabisabi926/faststrm/internal/service/pwdcrypto"
)

// TestHandlerRaces_Smoke 是一个仅用于「CI -race 下暴露并发问题」的冒烟测试。
//
// 覆盖到的已知 race 来源：
//   - config.Get() 与 MutateAdmin/MutateSettings 对 appCfg/Admin/Settings 的并发读写；
//   - ChangePassword/ChangeCredentials 对 Admin 的写入；
//   - Login/Health/Get 并发读。
//
// 设计上尽量「只做全局安全的读/写 + 纯 handler 执行」，避免依赖磁盘文件路径，
// 防止与同包其他测试共享进程环境时出现初始化交错（-coverpkg + -race 下更敏感）。
func TestHandlerRaces_Smoke(t *testing.T) {
	salt := "test_salt_32b_padding______"
	// 初始化全局 config（与 setupAuthTestCfg 保持一致的轻量模式：不走磁盘 JSON）
	cfg := &config.AppConfig{
		Salt: salt,
		Admin: &model.AppConfig{
			Username: "admin",
			Password: pwdcrypto.HashPassword(salt, "admin"),
		},
		Settings: model.DefaultSettings(),
	}
	config.SetForTest(cfg)

	issuer := auth.NewTokenIssuer([]byte("race-test-secret-32b-pad_______"))

	const rounds = 80

	loginH := Login(issuer)
	chpwdH := ChangePassword()
	chcredH := ChangeCredentials()
	healthH := Health

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

	// Goroutine A: Login
	spawn(func() {
		body := `{"username":"admin","password":"admin"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		loginH(w, r)
	})

	// Goroutine B: ChangePassword (admin<->newpass12 来回切换，前置校验失败时由 handler 返回 4xx，
	// 只要不 panic / 不出现 race，对测试目的而言就满足)
	spawn(func() {
		body := `{"currentPassword":"admin","newPassword":"newpass12"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		chpwdH(w, r)

		body2 := `{"currentPassword":"newpass12","newPassword":"admin"}`
		w2 := httptest.NewRecorder()
		r2 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body2))
		r2.Header.Set("Content-Type", "application/json")
		chpwdH(w2, r2)
	})

	// Goroutine C: ChangeCredentials (用户名/密码整体切换)
	spawn(func() {
		body := `{"currentPassword":"admin","newUsername":"userA","newPassword":"credPass","confirmPassword":"credPass"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		chcredH(w, r)

		body2 := `{"currentPassword":"credPass","newUsername":"admin","newPassword":"admin","confirmPassword":"admin"}`
		w2 := httptest.NewRecorder()
		r2 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body2))
		r2.Header.Set("Content-Type", "application/json")
		chcredH(w2, r2)
	})

	// Goroutine D: HealthCheck (只读路径)
	spawn(func() {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		healthH(w, r)
	})

	// Goroutine E: config.Get 快照读 (与写入并发的核心 race 场景)
	spawn(func() {
		cfg := config.Get()
		_ = cfg.Admin
		_ = cfg.Settings
		_ = cfg.Salt
	})

	// Goroutine F: MutateSettings copy-on-write 写 (与 Get/Login 并发)
	spawn(func() {
		_ = config.MutateSettings(func(s *model.Settings) {
			s.Strm.StrmUrlTemplate = "/ping"
		}, false)
		_ = config.MutateSettings(func(s *model.Settings) {
			s.Strm.StrmUrlTemplate = ""
		}, false)
	})

	close(startBarrier)
	wg.Wait()
}
