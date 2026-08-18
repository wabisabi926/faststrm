package logger

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	logger *zap.Logger
	sugar  *zap.SugaredLogger
)

// InitLogger 初始化 zap 日志
// logDir: 日志文件目录，为空则只输出到 stdout
func InitLogger(logDir string, level zapcore.Level) {
	var cores []zapcore.Core

	// 控制台输出
	consoleEncoder := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	consoleCore := zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), level)
	cores = append(cores, consoleCore)

	// 文件输出（如果指定了目录）
	if logDir != "" {
		if err := os.MkdirAll(logDir, 0755); err == nil {
			logFile := filepath.Join(logDir, "app.log")
			f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err == nil {
				fileEncoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
				fileCore := zapcore.NewCore(fileEncoder, zapcore.AddSync(f), level)
				cores = append(cores, fileCore)
			}
		}
	}

	core := zapcore.NewTee(cores...)
	logger = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
	sugar = logger.Sugar()
}

// L 获取 *zap.Logger
func L() *zap.Logger {
	if logger == nil {
		InitLogger("", zapcore.InfoLevel)
	}
	return logger
}

// S 获取 *zap.SugaredLogger
func S() *zap.SugaredLogger {
	if sugar == nil {
		InitLogger("", zapcore.InfoLevel)
	}
	return sugar
}

// Sync 刷新日志缓冲区
func Sync() {
	if logger != nil {
		_ = logger.Sync()
	}
}
