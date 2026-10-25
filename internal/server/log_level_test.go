package server

import (
	"testing"

	"go.uber.org/zap/zapcore"
)

func TestLogLevelFromEnv(t *testing.T) {
	cases := []struct {
		env  string
		want zapcore.Level
	}{
		{"", zapcore.InfoLevel},
		{"debug", zapcore.DebugLevel},
		{"DEBUG", zapcore.DebugLevel},
		{"warn", zapcore.WarnLevel},
		{"warning", zapcore.WarnLevel},
		{"error", zapcore.ErrorLevel},
		{"unknown", zapcore.InfoLevel},
	}
	for _, c := range cases {
		t.Setenv("FASTSTRM_LOG_LEVEL", c.env)
		if got := logLevelFromEnv(); got != c.want {
			t.Errorf("FASTSTRM_LOG_LEVEL=%q -> %v, want %v", c.env, got, c.want)
		}
	}
}
