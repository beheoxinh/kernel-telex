package main

import (
	"log"
	"os"
	"path/filepath"
	"syscall"

	"ktelex/config"
)

// Per-keystroke tracing used to write ~10 synchronous log lines per key into
// an ever-growing /tmp/ktelex.log. It is now opt-in:
//   - environment variable KTELEX_DEBUG=1, or
//   - an empty file ~/.config/ibus-ktelex/debug (checked at startup).
//
// Anomalies (watchdog fires, swallowed echoes, runaway BS) are always logged.
var debugEnabled = func() bool {
	if os.Getenv("KTELEX_DEBUG") != "" {
		return true
	}
	_, err := os.Stat(filepath.Join(config.GetConfigDir("ktelex"), "debug"))
	return err == nil
}()

func debugf(format string, args ...interface{}) {
	if debugEnabled {
		log.Printf(format, args...)
	}
}

const kLogRotateBytes = 8 << 20

// openLogFile opens the engine log, rotating it once it exceeds 8MB.
// The log lives in shared /tmp, so it is private to the user (0600) and a
// symlink planted at the path is refused instead of followed.
func openLogFile(path string) (*os.File, error) {
	if st, err := os.Lstat(path); err == nil && st.Mode().IsRegular() && st.Size() > kLogRotateBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() || int(st.Sys().(*syscall.Stat_t).Uid) != os.Getuid() {
		f.Close()
		return nil, os.ErrPermission
	}
	_ = f.Chmod(0600) // tighten a log created by an older build (0644)
	_ = os.Chmod(path+".1", 0600)
	return f, nil
}
