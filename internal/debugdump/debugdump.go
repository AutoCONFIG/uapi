package debugdump

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/google/uuid"
)

const (
	defaultQueueSize = 1000
)

type Config struct {
	Enabled      bool
	Dir          string
	QueueMaxSize int
}

type Entry struct {
	Timestamp        string                 `json:"timestamp"`
	Side             string                 `json:"side"`
	Category         string                 `json:"category"`
	Span             string                 `json:"span,omitempty"`
	TraceID          string                 `json:"trace_id,omitempty"`
	GatewayRequestID string                 `json:"gateway_request_id,omitempty"`
	RelayRequestID   string                 `json:"relay_request_id,omitempty"`
	RelayNodeID      string                 `json:"relay_node_id,omitempty"`
	Method           string                 `json:"method,omitempty"`
	Path             string                 `json:"path,omitempty"`
	URL              string                 `json:"url,omitempty"`
	Endpoint         string                 `json:"endpoint,omitempty"`
	Status           int                    `json:"status,omitempty"`
	LatencyMS        int64                  `json:"latency_ms,omitempty"`
	Model            string                 `json:"model,omitempty"`
	RoutedModel      string                 `json:"routed_model,omitempty"`
	ChannelID        string                 `json:"channel_id,omitempty"`
	AccountID        string                 `json:"account_id,omitempty"`
	Error            string                 `json:"error,omitempty"`
	DumpPath         string                 `json:"dump_path,omitempty"`
	Extra            map[string]interface{} `json:"extra,omitempty"`
}

type asyncWrite struct {
	path string
	body []byte
	mode os.FileMode
}

var state struct {
	mu      sync.RWMutex
	enabled bool
	dir     string
	queue   chan asyncWrite
	once    sync.Once
}

func Configure(cfg Config) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.enabled = cfg.Enabled && strings.TrimSpace(cfg.Dir) != ""
	state.dir = strings.TrimSpace(cfg.Dir)
	if !state.enabled {
		return
	}
	size := cfg.QueueMaxSize
	if size <= 0 {
		size = defaultQueueSize
	}
	if state.queue == nil {
		state.queue = make(chan asyncWrite, size)
		state.once.Do(func() { go writeLoop() })
	}
}

func Enabled() bool {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.enabled
}

func BaseDir() string {
	state.mu.RLock()
	defer state.mu.RUnlock()
	if !state.enabled {
		return ""
	}
	return state.dir
}

func DayDir(t time.Time) string {
	base := BaseDir()
	if base == "" {
		return ""
	}
	return filepath.Join(filepath.Clean(base), t.Local().Format("2006-01-02"))
}

func EntryName(t time.Time, id string) string {
	id = SafeName(id)
	if id == "" {
		id = uuid.NewString()
	}
	return t.Local().Format("20060102T150405.000000000-0700") + "-" + id
}

func EntryDir(t time.Time, parts ...string) string {
	dayDir := DayDir(t)
	if dayDir == "" {
		return ""
	}
	clean := []string{dayDir}
	for _, part := range parts {
		if safe := SafePathPart(part); safe != "" {
			clean = append(clean, safe)
		}
	}
	return filepath.Join(clean...)
}

func WriteFile(dir, name string, body []byte) {
	if dir == "" || name == "" {
		return
	}
	name = CleanRelativePath(name)
	if name == "" {
		return
	}
	path := filepath.Join(dir, name)
	queueWrite(path, body, 0644)
}

func WriteJSON(dir, name string, value interface{}) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		logger.Warnf("debug_dump", "marshal json dump failed", logger.Err(err), logger.F("file", name))
		return
	}
	WriteFile(dir, name, append(raw, '\n'))
}

func AppendIndex(t time.Time, entry Entry) {
	dayDir := DayDir(t)
	if dayDir == "" {
		return
	}
	if entry.Timestamp == "" {
		entry.Timestamp = t.Local().Format(time.RFC3339Nano)
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		logger.Warnf("debug_dump", "marshal index entry failed", logger.Err(err))
		return
	}
	queueWrite(filepath.Join(dayDir, "index.jsonl"), append(raw, '\n'), 0644)
}

func ExtractTarGz(dst string, body []byte) error {
	if dst == "" {
		return fmt.Errorf("empty destination")
	}
	gr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := CleanRelativePath(header.Name)
		if name == "" {
			continue
		}
		path := filepath.Join(dst, name)
		if header.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(f, tr)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
}

func SafeName(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func SafePathPart(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.Trim(value, "/ ")
	if value == "" || value == "." || value == ".." || strings.Contains(value, "/") {
		return ""
	}
	return SafeName(value)
}

func CleanRelativePath(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "/")
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
		return ""
	}
	return strings.ReplaceAll(clean, "\\", "/")
}

func queueWrite(path string, body []byte, mode os.FileMode) {
	state.mu.RLock()
	enabled := state.enabled
	queue := state.queue
	state.mu.RUnlock()
	if !enabled || queue == nil || path == "" {
		return
	}
	item := asyncWrite{path: path, body: append([]byte(nil), body...), mode: mode}
	select {
	case queue <- item:
	default:
		logger.Warnf("debug_dump", "drop async dump write because queue is full", logger.F("path", path))
	}
}

func writeLoop() {
	for item := range state.queue {
		if item.path == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(item.path), 0755); err != nil {
			logger.Warnf("debug_dump", "create dump parent dir failed", logger.Err(err), logger.F("path", item.path))
			continue
		}
		if strings.HasSuffix(item.path, ".jsonl") {
			f, err := os.OpenFile(item.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, item.mode)
			if err != nil {
				logger.Warnf("debug_dump", "open jsonl dump failed", logger.Err(err), logger.F("path", item.path))
				continue
			}
			_, writeErr := f.Write(item.body)
			closeErr := f.Close()
			if writeErr != nil {
				logger.Warnf("debug_dump", "append jsonl dump failed", logger.Err(writeErr), logger.F("path", item.path))
			} else if closeErr != nil {
				logger.Warnf("debug_dump", "close jsonl dump failed", logger.Err(closeErr), logger.F("path", item.path))
			}
			continue
		}
		if err := os.WriteFile(item.path, item.body, item.mode); err != nil {
			logger.Warnf("debug_dump", "write dump failed", logger.Err(err), logger.F("path", item.path))
		}
	}
}
