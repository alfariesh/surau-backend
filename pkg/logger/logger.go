package logger

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"
)

// Interface -.
type Interface interface {
	Debug(message any, args ...any)
	Info(message string, args ...any)
	Warn(message string, args ...any)
	Error(message any, args ...any)
	Fatal(message any, args ...any)
	// WithField returns a child logger that stamps the given field on every
	// line — the correlation hook for request_id / trace_id (F1-B).
	WithField(key string, value any) Interface
}

// Logger -.
type Logger struct {
	logger *zerolog.Logger
}

var _ Interface = (*Logger)(nil)

// NewWithWriter is New with a custom sink; tests use it to capture output.
func NewWithWriter(level string, out io.Writer) *Logger {
	base := New(level)
	child := base.logger.Output(out)

	return &Logger{logger: &child}
}

// New -.
func New(level string) *Logger {
	var l zerolog.Level

	switch strings.ToLower(level) {
	case "error":
		l = zerolog.ErrorLevel
	case "warn":
		l = zerolog.WarnLevel
	case "info":
		l = zerolog.InfoLevel
	case "debug":
		l = zerolog.DebugLevel
	default:
		l = zerolog.InfoLevel
	}

	zerolog.SetGlobalLevel(l)

	skipFrameCount := 3
	logger := zerolog.
		New(os.Stdout).
		With().
		Timestamp().
		CallerWithSkipFrameCount(zerolog.CallerSkipFrameCount + skipFrameCount).
		Logger()

	return &Logger{
		logger: new(logger),
	}
}

// WithField -.
func (l *Logger) WithField(key string, value any) Interface {
	child := l.logger.With().Interface(key, value).Logger()

	return &Logger{logger: &child}
}

// Debug -.
func (l *Logger) Debug(message any, args ...any) {
	l.msg(zerolog.DebugLevel, message, args...)
}

// Info -.
func (l *Logger) Info(message string, args ...any) {
	l.log(zerolog.InfoLevel, message, args...)
}

// Warn -.
func (l *Logger) Warn(message string, args ...any) {
	l.log(zerolog.WarnLevel, message, args...)
}

// Error -.
func (l *Logger) Error(message any, args ...any) {
	l.msg(zerolog.ErrorLevel, message, args...)
}

// Fatal -.
func (l *Logger) Fatal(message any, args ...any) {
	l.msg(zerolog.FatalLevel, message, args...)

	os.Exit(1)
}

func (l *Logger) log(level zerolog.Level, message string, args ...any) {
	if len(args) == 0 {
		l.logger.WithLevel(level).Msg(message)
	} else {
		l.logger.WithLevel(level).Msgf(message, args...)
	}
}

func (l *Logger) msg(level zerolog.Level, message any, args ...any) {
	switch msg := message.(type) {
	case error:
		l.log(level, "%s", errorMessage(msg, args...))
	case string:
		l.log(level, msg, args...)
	default:
		l.log(level, fmt.Sprintf("%s message %v has unknown type %v", level, message, msg), args...)
	}
}

func errorMessage(err error, args ...any) string {
	if len(args) == 0 {
		return err.Error()
	}

	context := fmt.Sprint(args...)
	if format, ok := args[0].(string); ok {
		context = format
		if len(args) > 1 && strings.Contains(format, "%") {
			context = fmt.Sprintf(format, args[1:]...)
		}
	}

	if strings.TrimSpace(context) == "" {
		return err.Error()
	}

	return fmt.Sprintf("%s: %s", context, err)
}
