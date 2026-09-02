package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
//   - Login/HealthCheck/Get 并发读。
//
// 本地 Windows 没 gcc 跑不了 -race，但 CI 会用 `-race` 跑这条作为兜底。
func TestHandlerRaces_Smoke(t *testing.T) {
	dir := t.TempDir()
	salt := "test_salt_32b_padding______"
	cfgDir := dir + "/cfg"
	dataDir := dir + "/data"
	mkdirAll(t, cfgDir, dataDir)

	settings := model.DefaultSettings()
	settingsPath := cfgDir + "/settings.json"
	writeFileJSON(t, settingsPath, settings)

	admin := &model.AppConfig{Username: "admin", Password: pwdcrypto.HashPassword(salt, "admin")}
	adminPath := cfgDir + "/config.json"
	writeFileJSON(t, adminPath, admin)

	writeFile(t, cfgDir+"/.salt", []byte(salt))

	paths := config.AppConfigPaths{
		DataDir:      dataDir,
		ConfigDir:    cfgDir,
		CacheDir:     dataDir + "/cache",
		DBDir:        dataDir + "/db",
		SettingsPath: settingsPath,
		ConfigPath:   adminPath,
		SaltPath:     cfgDir + "/.salt",
		DefaultDir:   dir,
		LogDir:       dataDir + "/logs",
	}
	mkdirAll(t, paths.CacheDir, paths.DBDir, paths.LogDir)

	loaded, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	snap := loaded.Snapshot()
	snap.Salt = salt
	config.Replace(snap)

	issuer := auth.NewTokenIssuer([]byte("race-test-secret-32b-pad_______"))

	const rounds = 120

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

	spawn(func() {
		body := `{"username":"admin","password":"admin"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		loginH(w, r)
		_ = w.Code
	})

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

	spawn(func() {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		healthH(w, r)
	})

	spawn(func() {
		cfg := config.Get()
		_ = cfg.Admin
		_ = cfg.Settings
	})

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

func writeFileJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	writeFile(t, path, b)
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mkdirAll(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
}