package logger

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap/zapcore"
)

func TestInitLogger_StdoutOnly(t *testing.T) {
	InitLogger("", zapcore.DebugLevel)
	if logger == nil {
		t.Error("logger should not be nil after InitLogger")
	}
	if sugar == nil {
		t.Error("sugar should not be nil after InitLogger")
	}
}

func TestInitLogger_WithFileDir(t *testing.T) {
	// 不用 t.TempDir() 避免文件句柄未关闭导致清理失败
	dir := filepath.Join(os.TempDir(), "faststrm-logger-test")
	_ = os.MkdirAll(dir, 0755)
	InitLogger(dir, zapcore.InfoLevel)
	if logger == nil {
		t.Error("logger should not be nil")
	}
	Sync()
	// 验证日志文件已创建
	if _, err := os.Stat(filepath.Join(dir, "app.log")); err != nil {
		t.Errorf("app.log should exist: %v", err)
	}
}

func TestL_ReturnsLogger(t *testing.T) {
	logger = nil
	sugar = nil
	l := L()
	if l == nil {
		t.Error("L() should not return nil")
	}
}

func TestS_ReturnsSugar(t *testing.T) {
	logger = nil
	sugar = nil
	s := S()
	if s == nil {
		t.Error("S() should not return nil")
	}
}

func TestSync_NoPanic(t *testing.T) {
	InitLogger("", zapcore.InfoLevel)
	Sync()
}
