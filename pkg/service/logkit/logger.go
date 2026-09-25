package logkit

// logger的抽象

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/library/stringkit"
	"github.com/example/go-frame/pkg/library/timekit"
	"github.com/example/go-frame/pkg/service/configkit"
	"gopkg.in/natefinch/lumberjack.v2"
)

var once sync.Once
var fileLogger *slog.Logger
var consoleEnabled bool

func Init() {
	once.Do(func() {
		consoleEnabled = configkit.GetBool(configkey.LogConsole, true)

		var level slog.Level
		switch configkit.GetString(configkey.LogLevel) {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		default:
			level = slog.LevelInfo
		}
		option := &slog.HandlerOptions{
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey {
					a.Value = slog.AnyValue(a.Value.Time().Format(timekit.TimeLayout))
				}
				return a
			},
			Level: level,
		}
		if consoleEnabled {
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, option)))
		} else {
			slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, option)))
		}
		if configkit.Exist(configkey.LogPath) {
			switch configkit.GetString(configkey.LogType) {
			case "json":
				fileLogger = slog.New(slog.NewJSONHandler(getRollWriter(), option))
			default:
				fileLogger = slog.New(slog.NewTextHandler(getRollWriter(), option))
			}
		}
	})
}

// ConsoleEnabled 控制台输出是否开启（log.console，默认 true）。
// 供访问日志这类需要「控制台彩色单行」的场景判定，避免绕过开关直写 stderr。
func ConsoleEnabled() bool {
	Init()
	return consoleEnabled
}

func getRollWriter() io.Writer {
	filename := configkit.GetString(configkey.LogName)
	filepath := configkit.GetString(configkey.LogPath)
	if stringkit.IsNull(filepath) {
		filepath = configkit.GetString(configkey.ProjectDir) + "/log"
	}
	filepath = stringkit.ClearFilePath(filepath)
	config := &lumberjack.Logger{
		Filename:   filepath + "/" + filename + ".log",
		MaxSize:    configkit.GetInt(configkey.LogMaxSize),
		MaxBackups: configkit.GetInt(configkey.LogMaxBackups),
		MaxAge:     configkit.GetInt(configkey.LogMaxRemain),
		LocalTime:  true,
		Compress:   true,
	}
	return config
}

func Debug(msg string, args ...any) {
	slog.Debug(msg, args...)
	if fileLogger != nil {
		fileLogger.Debug(msg, args...)
	}
}

// DebugEnabled 当前日志级别是否输出 Debug。供调用方在拼装昂贵日志参数
// （如 args 的 JSON 序列化）之前短路，避免级别不够时白白付出序列化开销。
func DebugEnabled() bool {
	Init()
	return slog.Default().Enabled(context.Background(), slog.LevelDebug)
}
func Info(msg string, args ...any) {
	slog.Info(msg, args...)
	if fileLogger != nil {
		fileLogger.Info(msg, args...)
	}
}

func InfoFile(msg string, args ...any) {
	if fileLogger != nil {
		fileLogger.Info(msg, args...)
	}
}
func Error(msg string, args ...any) {
	ErrorException(exception.New(msg, 2), args...)
}

func ErrorException(ex exception.Exception, args ...any) {
	args = append(args, "stackTrace", ex.Stack)
	ErrorOrigin(ex.Msg, args...)
}

func ErrorOrigin(msg string, args ...any) {
	slog.Error(msg, args...)
	if fileLogger != nil {
		fileLogger.Error(msg, args...)
	}
}

// Fatal 记录错误日志后以退出码 1 结束进程（跳过 defer，用于不可恢复的错误）。
// 典型场景：HTTP serve 失败（端口被占用等）——进程已无法提供服务，继续运行没有意义。
func Fatal(msg string, args ...any) {
	ErrorOrigin(msg, args...)
	os.Exit(1)
}
