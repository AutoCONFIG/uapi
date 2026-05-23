package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

type Logger struct {
	component string
	state     *state
}

type state struct {
	mu    sync.RWMutex
	level Level
	out   io.Writer
}

type Field struct {
	Key   string
	Value interface{}
}

var defaultState = &state{level: Info, out: os.Stdout}
var defaultLogger = &Logger{state: defaultState}

func Configure(level string) {
	defaultLogger.SetLevel(level)
}

func Default() *Logger { return defaultLogger }

func Component(name string) *Logger {
	return defaultLogger.WithComponent(name)
}

func F(key string, value interface{}) Field {
	return Field{Key: key, Value: value}
}

func Err(err error) Field {
	if err == nil {
		return Field{Key: "error", Value: nil}
	}
	return Field{Key: "error", Value: Redact(err.Error())}
}

func (l *Logger) WithComponent(name string) *Logger {
	return &Logger{component: name, state: l.state}
}

func (l *Logger) SetLevel(level string) {
	parsed := parseLevel(level)
	l.state.mu.Lock()
	l.state.level = parsed
	l.state.mu.Unlock()
}

func Debugf(component, msg string, fields ...Field) { Component(component).Debug(msg, fields...) }
func Infof(component, msg string, fields ...Field)  { Component(component).Info(msg, fields...) }
func Warnf(component, msg string, fields ...Field)  { Component(component).Warn(msg, fields...) }
func Errorf(component, msg string, fields ...Field) { Component(component).Error(msg, fields...) }

func (l *Logger) Debug(msg string, fields ...Field) { l.log(Debug, msg, fields...) }
func (l *Logger) Info(msg string, fields ...Field)  { l.log(Info, msg, fields...) }
func (l *Logger) Warn(msg string, fields ...Field)  { l.log(Warn, msg, fields...) }
func (l *Logger) Error(msg string, fields ...Field) { l.log(Error, msg, fields...) }

func (l *Logger) Panic(component, msg string, recovered interface{}, fields ...Field) {
	fields = append(fields, F("panic", fmt.Sprint(recovered)), F("stack", string(debug.Stack())))
	Component(component).Error(msg, fields...)
}

func (l *Logger) log(level Level, msg string, fields ...Field) {
	if !l.enabled(level) {
		return
	}
	record := map[string]interface{}{
		"ts":    time.Now().UTC().Format(time.RFC3339Nano),
		"level": level.String(),
		"msg":   msg,
	}
	if l.component != "" {
		record["component"] = l.component
	}
	for _, field := range fields {
		if field.Key == "" {
			continue
		}
		record[field.Key] = sanitizeValue(field.Value)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		log.Printf("logger marshal failed: %v", err)
		return
	}
	l.state.mu.Lock()
	defer l.state.mu.Unlock()
	fmt.Fprintln(l.state.out, string(encoded))
}

func sanitizeValue(v interface{}) interface{} {
	switch x := v.(type) {
	case string:
		return redactString(x)
	case []string:
		out := make([]string, len(x))
		for i, item := range x {
			out[i] = redactString(item)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for key, val := range x {
			if isSecretKey(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = sanitizeValue(val)
		}
		return out
	default:
		return v
	}
}

func Redact(s string) string {
	return redactString(s)
}

func isSecretKey(key string) bool {
	k := strings.ToLower(key)
	switch k {
	case "authorization", "api_key", "apikey", "key", "client_secret", "access_token", "refresh_token", "id_token", "password", "secret":
		return true
	}
	for _, suffix := range []string{"_secret", "_password", "_api_key", "_access_token", "_refresh_token", "_id_token"} {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

func redactString(s string) string {
	for _, marker := range []string{"Bearer ", "sk-", "sk-ant-", "ya29.", "AIza", "1//"} {
		for {
			start := strings.Index(s, marker)
			if start < 0 {
				break
			}
			end := start + len(marker)
			for end < len(s) && !strings.ContainsRune(" \t\r\n\"'`,;}", rune(s[end])) {
				end++
			}
			if end <= start || strings.HasPrefix(s[start:], "[redacted]") {
				break
			}
			s = s[:start] + "[redacted]" + s[end:]
		}
	}
	s = redactKeyValueSecrets(s)
	return s
}

var secretKVPattern = regexp.MustCompile(`(?i)\b(access_token|refresh_token|id_token|api_key|apikey|client_secret|authorization|password|secret)\b\s*[:=]\s*["']?[^"'\s,;}]+`)

func redactKeyValueSecrets(s string) string {
	return secretKVPattern.ReplaceAllStringFunc(s, func(match string) string {
		for i, r := range match {
			if r == ':' || r == '=' {
				return match[:i+1] + "[redacted]"
			}
		}
		return "[redacted]"
	})
}

func (l *Logger) enabled(level Level) bool {
	l.state.mu.RLock()
	min := l.state.level
	l.state.mu.RUnlock()
	return level >= min
}

func parseLevel(level string) Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return Debug
	case "warn", "warning":
		return Warn
	case "error":
		return Error
	default:
		return Info
	}
}

func (l Level) String() string {
	switch l {
	case Debug:
		return "debug"
	case Warn:
		return "warn"
	case Error:
		return "error"
	default:
		return "info"
	}
}
