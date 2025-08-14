package log

import (
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var Default *Logger

// Logger 日志结构体
type Logger struct {
	*zap.Logger
}

func (l *Logger) Printf(format string, v ...any) {
	l.Sugar().Infof(format, v...)
}

func InitZap(path string, logWarn bool, env string) *Logger {
	// 日志地址 "out.log" 自定义
	lp := path

	// 调试信息：打印日志路径
	if lp != "" {
		println("日志文件路径:", lp)
	} else {
		println("未指定日志文件路径，日志将只输出到控制台")
	}
	// 日志级别 DEBUG,ERROR, INFO

	lv := "info"

	if logWarn {
		lv = "warn"
	}

	var level zapcore.Level
	//debug<info<warn<error<fatal<panic
	switch lv {
	case "debug":
		level = zap.DebugLevel
	case "info":
		level = zap.InfoLevel
	case "warn":
		level = zap.WarnLevel
	case "error":
		level = zap.ErrorLevel
	default:
		level = zap.InfoLevel
	}
	hook := lumberjack.Logger{
		Filename: lp, // 日志文件路径
		// MaxSize:    100,   // 每个日志文件保存的最大尺寸 单位：M
		MaxBackups: 30,    // 日志文件最多保存多少个备份
		MaxAge:     30,    // 文件最多保存多少天
		Compress:   false, // 是否压缩
	}

	var encoder zapcore.Encoder
	if path == "" {
		encoder = zapcore.NewConsoleEncoder(zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "Logger",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseColorLevelEncoder,
			EncodeTime:     timeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.FullCallerEncoder,
		})
	} else {
		encoder = zapcore.NewJSONEncoder(zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			FunctionKey:    zapcore.OmitKey,
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     timeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		})
	}
	core := zapcore.NewCore(
		encoder, // 编码器配置
		zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout), zapcore.AddSync(&hook)), // 打印到控制台和文件
		level, // 日志级别
	)
	if env != "prod" {
		return &Logger{zap.New(core, zap.Development(), zap.AddCaller(), zap.AddCallerSkip(1), zap.AddStacktrace(zap.ErrorLevel))}
	}
	return &Logger{zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1), zap.AddStacktrace(zap.ErrorLevel))}

}

// 自定义时间编码器
func timeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	//enc.AppendString(t.Format("2006-01-02 15:04:05"))
	enc.AppendString(t.Format("2006-01-02 15:04:05"))
}

// Info logs a message at the info level.
func Info(msg string, fields ...zap.Field) {
	if Default != nil {
		Default.Info(msg, fields...)
	}
}

// Debug logs a message at the debug level.
func Debug(msg string, fields ...zap.Field) {
	if Default != nil {
		Default.Debug(msg, fields...)
	}
}

// Warn logs a message at the warn level.
func Warn(msg string, fields ...zap.Field) {
	if Default != nil {
		Default.Warn(msg, fields...)
	}
}

// Error logs a message at the error level.
func Error(msg string, fields ...zap.Field) {
	if Default != nil {
		Default.Error(msg, fields...)
	}
}

func Fatal(msg string, fields ...zap.Field) {
	if Default != nil {
		Default.Fatal(msg, fields...)
	}
}
