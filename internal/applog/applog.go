// Package applog is a tiny process-wide logger shared by the device layer and
// the GUI. Every line lands in an in-memory ring buffer (the "Log" tab renders
// it) and, once Init has run, in a rotating file under the OS cache dir and on
// stderr. It is pure stdlib and safe to call before Init: the ring still
// fills, only the file and stderr sinks wait for it.
package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ringMax is how many recent lines the in-memory buffer keeps for the GUI.
const ringMax = 2000

// rotateAt is the file size past which Init renames the old log to "<name>.1".
const rotateAt = 1 << 20 // 1 MiB

// Level orders the severities from most to least verbose.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "?????"
	}
}

// ParseLevel maps a level name (any case) to a Level. It returns
// (LevelInfo, false) for anything it does not recognise, so a bad value
// from an env var or UI selector falls back to the default level instead of
// silently going quiet or noisy.
func ParseLevel(s string) (Level, bool) {
	switch strings.ToUpper(s) {
	case "DEBUG":
		return LevelDebug, true
	case "INFO":
		return LevelInfo, true
	case "WARN", "WARNING":
		return LevelWarn, true
	case "ERROR":
		return LevelError, true
	default:
		return LevelInfo, false
	}
}

var (
	mu     sync.Mutex
	file   *os.File
	ring   []string
	path   string
	stderr bool // mirror every line to os.Stderr; enabled by Init
	level  = defaultLevel()
)

// defaultLevel is LevelInfo, unless the K724_LOG_LEVEL environment variable
// names a valid level — set before a rebuild is needed to capture a bug
// report at DEBUG (see docs/MISSING_FEATURES.md "Logging — finer levels").
func defaultLevel() Level {
	if v, ok := ParseLevel(os.Getenv("K724_LOG_LEVEL")); ok {
		return v
	}
	return LevelInfo
}

// SetLevel changes the minimum level that reaches the ring buffer, log file,
// and stderr. Lines below it are dropped before formatting. The GUI's Log
// tab exposes this so a bug report can be captured at DEBUG without a
// rebuild.
func SetLevel(l Level) {
	mu.Lock()
	level = l
	mu.Unlock()
}

// GetLevel returns the current minimum level.
func GetLevel() Level {
	mu.Lock()
	defer mu.Unlock()
	return level
}

// Init opens (creating if needed) the log file "<cache>/k724-tool/<name>.log"
// and appends to it. It returns the file path, or an error if the directory or
// file could not be created — in which case logging still works, minus the
// file sink. Safe to call more than once.
func Init(name string) (string, error) {
	mu.Lock()
	defer mu.Unlock()

	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "k724-tool")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	p := filepath.Join(dir, name+".log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > rotateAt {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	if file != nil {
		_ = file.Close()
	}
	file = f
	path = p
	stderr = true
	return p, nil
}

// Path is the log file path, or a placeholder if Init has not succeeded.
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	if path == "" {
		return "(no file — logging to memory and stderr only)"
	}
	return path
}

// Lines returns a snapshot copy of the ring buffer, oldest first.
func Lines() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, len(ring))
	copy(out, ring)
	return out
}

func emit(lvl Level, format string, args ...any) {
	mu.Lock()
	if lvl < level {
		mu.Unlock()
		return
	}
	mu.Unlock()

	line := fmt.Sprintf("%s  %-5s %s",
		time.Now().Format("2006-01-02 15:04:05.000"),
		lvl,
		fmt.Sprintf(format, args...))

	mu.Lock()
	ring = append(ring, line)
	if len(ring) > ringMax {
		ring = append([]string(nil), ring[len(ring)-ringMax:]...)
	}
	if file != nil {
		fmt.Fprintln(file, line)
	}
	toStderr := stderr
	mu.Unlock()

	if toStderr {
		fmt.Fprintln(os.Stderr, line)
	}
}

// Debugf, Infof, Warnf and Errorf log one formatted line at the named level.
// A line below the current SetLevel threshold (default LevelInfo) is
// dropped before formatting its arguments.
func Debugf(format string, args ...any) { emit(LevelDebug, format, args...) }
func Infof(format string, args ...any)  { emit(LevelInfo, format, args...) }
func Warnf(format string, args ...any)  { emit(LevelWarn, format, args...) }
func Errorf(format string, args ...any) { emit(LevelError, format, args...) }
