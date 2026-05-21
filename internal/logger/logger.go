package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
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
	return Field{Key: "error", Value: err.Error()}
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
		record[field.Key] = field.Value
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
