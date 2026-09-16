package logger

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	logger *zap.Logger
	sugar  *zap.SugaredLogger
)

// 滚动参数
const (
	// RollMaxSizeMB 单个日志文件达到该大小(MB)则触发滚动
	RollMaxSizeMB = 50
	// RollMaxBackups 保留的滚动备份文件数（超出则删除最旧）
	RollMaxBackups = 5
	// RollMaxAgeDays 滚动文件保留天数（超出则删除）
	RollMaxAgeDays = 7
)

// InitLogger 初始化 zap 日志（文件输出接入 lumberjack 滚动，磁盘有界）
// logDir: 日志文件目录，为空则只输出到 stdout
func InitLogger(logDir string, level zapcore.Level) {
	var cores []zapcore.Core

	// 控制台输出
	consoleEncoder := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	consoleCore := zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), level)
	cores = append(cores, consoleCore)

	// 文件输出（如果指定了目录）：lumberjack 按大小/天数滚动并压缩，避免 app.log 无限增长
	if logDir != "" {
		if err := os.MkdirAll(logDir, 0755); err == nil {
			logFile := filepath.Join(logDir, "app.log")
			rotator := &lumberjack.Logger{
				Filename:   logFile,
				MaxSize:    RollMaxSizeMB, // MB
				MaxBackups: RollMaxBackups,
				MaxAge:     RollMaxAgeDays, // 天
				Compress:   true,           // 滚动后 gzip 压缩
			}
			fileEncoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
			fileCore := zapcore.NewCore(fileEncoder, zapcore.AddSync(rotator), level)
			cores = append(cores, fileCore)
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
