// Package logger slog 接线:console/JSON 输出 + ctx 惯用法 + 文件轮转。
// Options 提供 AddSource(调用点定位)与 StackOnError(Error 级自动附堆栈);
// 错误字段统一走 ErrAttrs(error_name/error_message)。
package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
)

// Options logger 构造选项。
type Options struct {
	Format       string // console (默认)、json 或 sls; New 未知值回退 console 并 Warn，Open 严格校验
	Environment  string // sls 的 env 字段；未设置时为 production
	Timezone     string // IANA 时区，空值使用记录原有时区；Open 严格校验，New 未知值回退并 Warn
	Level        string // debug/info/warn/error; New 回退 info 并发一条 Warn 记录,Open 和 SetLevel 严格拒绝未知值
	AddSource    bool   // 每条记录附 source.file/line(slog 原生)
	StackOnError bool   // Level>=Error 自动附 runtime 堆栈
}

// New 按 Options 建 logger(stdout)，默认 console。未知 Level 回退 info 并通过
// 新 logger 发出一条 Warn 记录，使误配可见（Open/SetLevel 仍严格报错）。
func New(opts Options) *slog.Logger {
	lg := slog.New(buildHandler(opts, consoleOutput(os.Stdout, opts.Format)))
	if _, err := strictLevel(opts.Level); err != nil {
		lg.Warn("logger: unknown level, using info", "level", opts.Level)
	}
	if err := validateFormat(opts.Format); err != nil {
		lg.Warn("logger: unknown format, using console", "format", opts.Format)
	}
	if _, err := logLocation(opts.Timezone); err != nil {
		lg.Warn("logger: unknown timezone, using record timezone", "timezone", opts.Timezone)
	}
	return lg
}

// buildHandler 组装输出 handler;StackOnError 时包一层堆栈注入 handler。
func buildHandler(opts Options, w io.Writer) slog.Handler {
	h := outputHandler(opts, w, parseLevel(opts.Level))
	if opts.StackOnError {
		return stackHandler{Handler: h}
	}
	return h
}

// stackHandler 对 Level>=Error 的记录追加 stack 字段。
type stackHandler struct {
	slog.Handler
	hasStack bool
}

var stackBuffers = sync.Pool{New: func() any { return new([8192]byte) }}

func (h stackHandler) Handle(ctx context.Context, r slog.Record) error {
	hasStack := h.hasStack
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "stack" {
			hasStack = true
			return false
		}
		return true
	})
	if r.Level >= slog.LevelError && !hasStack {
		buf := stackBuffers.Get().(*[8192]byte)
		n := runtime.Stack(buf[:], false)
		// The string owns its bytes; handlers may retain the record after Handle.
		stack := string(buf[:n])
		stackBuffers.Put(buf)
		r = r.Clone()
		r.AddAttrs(slog.String("stack", stack))
	}
	return h.Handler.Handle(ctx, r)
}

func (h stackHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hasStack := h.hasStack
	for _, a := range attrs {
		if a.Key == "stack" {
			hasStack = true
		}
	}
	return stackHandler{Handler: h.Handler.WithAttrs(attrs), hasStack: hasStack}
}

func (h stackHandler) WithGroup(name string) slog.Handler {
	return stackHandler{Handler: h.Handler.WithGroup(name), hasStack: h.hasStack}
}

// ErrAttrs 错误的结构化字段:error_name(具体类型名)+ error_message。
// 全库 error 级日志统一用此惯例;配 StackOnError 时堆栈自动携带。
func ErrAttrs(err error) []slog.Attr {
	if err == nil {
		return nil
	}
	return []slog.Attr{
		slog.String("error_name", fmt.Sprintf("%T", err)),
		slog.String("error_message", err.Error()),
	}
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type ctxKey struct{}

// IntoContext 把 logger 挂进 ctx。
func IntoContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext 取 ctx 内 logger;无则默认。
func FromContext(ctx context.Context) *slog.Logger {
	return FromContextOr(ctx, nil)
}

// FromContextOr prefers a context logger, then fallback, then slog.Default.
// A nil context is treated as containing no logger.
func FromContextOr(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if ctx != nil {
		if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
			return l
		}
	}
	if fallback != nil {
		return fallback
	}
	return slog.Default()
}
