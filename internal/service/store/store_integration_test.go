package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/pwdcrypto"
	"github.com/wabisabi926/faststrm/internal/service/task"
)

// 阶段 3 集成测试：AccountStore + SettingsStore + TasksStore 跨模块端到端持久化
// 重点：
//   1) 账号写入时 cookie/password 被加密（IsEncrypted==true），读取时自动还原
//   2) Settings 保存/读取/默认值填充
//   3) Tasks 原子写 Upsert/Delete/Round-trip，重启（新实例）后可恢复
func TestPhase3_StoreIntegration(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	salt := "phase3_test_salt_value_32bytes__" // 32 bytes

	t.Run("AccountStore round-trip with encryption", func(t *testing.T) {
		as := NewAccountStore(salt, cfgDir)
		accounts := []model.AccountInfo{
			{Account: "alice@115.com", Password: "p@ssw0rd", Cookie: "cookie_a=xxx; UID=u1", Name: "Alice"},
			{Account: "bob@115.com",   Password: "",        Cookie: "cookie_b=yyy; UID=u2", Name: "Bob"}, // 空密码不加密
		}
		if err := as.WriteAccounts(accounts); err != nil {
			t.Fatalf("WriteAccounts err: %v", err)
		}

		// 直接读原始文件，确认敏感字段被加密
		raw, err := os.ReadFile(filepath.Join(cfgDir, "account.json"))
		if err != nil { t.Fatalf("read file err: %v", err) }
		rawStr := string(raw)
		if !pwdcrypto.IsEncrypted(findField(rawStr, "alice@115.com", "password")) {
			t.Errorf("alice password should be encrypted on disk, got: %q", findField(rawStr, "alice@115.com", "password"))
		}
		if !pwdcrypto.IsEncrypted(findField(rawStr, "alice@115.com", "cookie")) {
			t.Errorf("alice cookie should be encrypted on disk, got first chars: %q", trunc(findField(rawStr, "alice@115.com", "cookie"), 18))
		}
		// bob password 空串 → 要么空字符串 "" 要么未加密（因为空串不会进入加密分支）
		if bobPwd := findField(rawStr, "bob@115.com", "password"); pwdcrypto.IsEncrypted(bobPwd) {
			t.Errorf("bob empty password should NOT be encrypted, got: %q", trunc(bobPwd, 20))
		}

		// 再读回来：应与原对象一致（自动解密）
		back, err := as.ReadAccounts()
		if err != nil { t.Fatalf("ReadAccounts err: %v", err) }
		if len(back) != 2 { t.Fatalf("want 2, got %d", len(back)) }
		// 按 Account 定位
		byAcct := map[string]model.AccountInfo{}
		for _, a := range back { byAcct[a.Account] = a }
		if alice, ok := byAcct["alice@115.com"]; !ok {
			t.Fatalf("alice missing")
		} else {
			if alice.Password != "p@ssw0rd"  { t.Errorf("alice password mismatch: %q", alice.Password) }
			if alice.Cookie   != "cookie_a=xxx; UID=u1" { t.Errorf("alice cookie mismatch: %q", alice.Cookie) }
			if alice.Name     != "Alice"     { t.Errorf("alice name mismatch") }
		}
		if bob, ok := byAcct["bob@115.com"]; !ok {
			t.Fatalf("bob missing")
		} else {
			if bob.Cookie != "cookie_b=yyy; UID=u2" { t.Errorf("bob cookie mismatch: %q", bob.Cookie) }
		}

		// 模拟"重启"：用新 Store 实例读同样文件 → 持久化一致
		as2 := NewAccountStore(salt, cfgDir)
		back2, err := as2.ReadAccounts()
		if err != nil { t.Fatalf("restart ReadAccounts err: %v", err) }
		if len(back2) != 2 { t.Errorf("restart accounts len want 2, got %d", len(back2)) }
		for _, a := range back2 {
			if a.Account != "alice@115.com" && a.Account != "bob@115.com" {
				t.Errorf("unexpected account: %+v", a)
			}
		}
	})

	t.Run("SettingsStore defaults + round-trip + restart", func(t *testing.T) {
		ss := NewSettingsStore(salt, cfgDir)

		// 首次读取（settings.json 不存在）→ 默认值
		def, err := ss.ReadSettings()
		if err != nil { t.Fatalf("read default settings err: %v", err) }
		if len(def.StrmExtensions) == 0 { t.Errorf("default StrmExtensions empty") }
		if len(def.DownloadExtensions) == 0 { t.Errorf("default DownloadExtensions empty") }
		// 默认内嵌 LifeMonitor.PollInterval 应有值
		if def.LifeMonitor.PollInterval <= 0 { t.Errorf("default LifeMonitor.PollInterval should be >0, got %d", def.LifeMonitor.PollInterval) }
		if len(def.Strm.ForceProxyUaTokens) == 0 { t.Errorf("default Strm.ForceProxyUaTokens empty") }

		// 修改并保存
		def.StrmPrefix = "http://custom:8090/strm"
		customExt := []string{"mp4", ".mkv", ".MOV"} // 故意含 . 前缀与大小写
		def.StrmExtensions = customExt
		def.Enable302 = true
		def.EnablePathEncoding = true
		if err := ss.SaveSettings(def); err != nil { t.Fatalf("SaveSettings err: %v", err) }

		// 读回来并验证
		back, err := ss.ReadSettings()
		if err != nil { t.Fatalf("ReadSettings back err: %v", err) }
		if back.StrmPrefix != "http://custom:8090/strm" { t.Errorf("StrmPrefix mismatch: %q", back.StrmPrefix) }
		if !back.Enable302 { t.Errorf("Enable302 should be true") }
		if !back.EnablePathEncoding { t.Errorf("EnablePathEncoding should be true") }
		// Extensions 保留原始内容（只在需要生成 strm 时 normalize）
		if len(back.StrmExtensions) != 3 { t.Errorf("StrmExtensions len want 3, got %d", len(back.StrmExtensions)) }

		// 模拟重启：新实例读
		ss2 := NewSettingsStore(salt, cfgDir)
		back2, err := ss2.ReadSettings()
		if err != nil { t.Fatalf("restart ReadSettings err: %v", err) }
		if back2.StrmPrefix != "http://custom:8090/strm" || !back2.Enable302 || !back2.EnablePathEncoding {
			t.Errorf("restart settings not persisted: StrmPrefix=%q Enable302=%v EnablePathEncoding=%v",
				back2.StrmPrefix, back2.Enable302, back2.EnablePathEncoding)
		}
	})

	t.Run("TasksStore upsert/delete/atomic restart", func(t *testing.T) {
		ts := NewTasksStore(cfgDir)

		// 首次读取（.tasks.json 不存在）→ 自动生成空数组
		list, err := ts.ReadTasks()
		if err != nil { t.Fatalf("initial ReadTasks err: %v", err) }
		if len(list) != 0 { t.Errorf("initial want empty, got %d", len(list)) }

		// Upsert: create
		t1 := task.Task{ID: "t_001", Name: "Scan Movies", Account: "alice@115.com",
			OriginPath: "/我的接收", TargetPath: "/mnt/nas/strm/alice", EnablePathEncoding: true}
		if err := ts.UpsertTask(t1); err != nil { t.Fatalf("Upsert create err: %v", err) }

		// Upsert: update (保留创建时间)
		first := mustFind(t, ts, "t_001")
		first.TargetPath = "/mnt/nas/strm/alice_v2"
		first.RemoveExtraFiles = true
		if err := ts.UpsertTask(*first); err != nil { t.Fatalf("Upsert update err: %v", err) }
		updated := mustFind(t, ts, "t_001")
		if updated.TargetPath != "/mnt/nas/strm/alice_v2" { t.Errorf("TargetPath update failed: %q", updated.TargetPath) }
		if updated.CreatedAt == 0 || updated.UpdatedAt == 0 || updated.UpdatedAt < updated.CreatedAt {
			t.Errorf("createdAt/updatedAt malformed: %+v", *updated)
		}

		// Upsert second task
		t2 := task.Task{ID: "t_002", Name: "TV Shows", Account: "bob@115.com",
			OriginPath: "/我的分享", TargetPath: "/mnt/nas/strm/bob"}
		if err := ts.UpsertTask(t2); err != nil { t.Fatalf("Upsert 2nd err: %v", err) }

		// 重启后一致性
		ts2 := NewTasksStore(cfgDir)
		list2, err := ts2.ReadTasks()
		if err != nil { t.Fatalf("restart ReadTasks err: %v", err) }
		if len(list2) != 2 { t.Fatalf("restart tasks len want 2, got %d", len(list2)) }

		// Delete
		ok, err := ts.DeleteTask("t_002")
		if !ok || err != nil { t.Errorf("DeleteTask ok=%v err=%v", ok, err) }
		remaining, err := ts.ReadTasks()
		if err != nil { t.Fatalf("ReadTasks after delete err: %v", err) }
		if len(remaining) != 1 || remaining[0].ID != "t_001" {
			t.Errorf("after delete expect 1=t_001, got %+v", remaining)
		}

		// Delete 不存在：false/nil
		ok, err = ts.DeleteTask("ghost")
		if ok || err != nil { t.Errorf("Delete ghost ok=%v err=%v (want false/nil)", ok, err) }
	})
}

// ----------------------------- helpers -----------------------------

// findField 从 JSON 文本中近似抽取指定 "account":"<acctName>" 对象下的字段值（仅用于本测试断言）
func findField(raw, acctName, field string) string {
	// 找 "account": "<acctName>"，然后在之后的窗口中找 "field": "value" 第一个匹配
	acctMarker := `"account": "` + acctName + `"`
	pos := indexOf(raw, acctMarker)
	if pos < 0 {
		// 兼容紧凑写法不带空格
		acctMarker = `"account":"` + acctName + `"`
		pos = indexOf(raw, acctMarker)
	}
	if pos < 0 { return "" }
	// 在之后 2000 字节窗口内找
	start := pos
	end := start + 2000
	if end > len(raw) { end = len(raw) }
	seg := raw[start:end]
	// 尝试 "field": "value" 和 "field":"value"
	for _, variant := range []string{`"` + field + `": "`, `"` + field + `":"`} {
		p := indexOf(seg, variant)
		if p < 0 { continue }
		p += len(variant)
		endP := indexOf(seg[p:], `"`)
		if endP < 0 { continue }
		return seg[p : p+endP]
	}
	return ""
}

func trunc(s string, n int) string {
	if n <= 0 { return s }
	if len(s) <= n { return s }
	return s[:n] + "..."
}

func indexOf(s, sub string) int {
	n := len(sub)
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub { return i }
	}
	return -1
}

func mustFind(t *testing.T, ts *TasksStore, id string) *task.Task {
	t.Helper()
	list, err := ts.ReadTasks()
	if err != nil { t.Fatalf("ReadTasks: %v", err) }
	for i := range list {
		if list[i].ID == id { return &list[i] }
	}
	t.Fatalf("task id=%q not found", id)
	return nil
}
