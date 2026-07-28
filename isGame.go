package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var knownGameLaunchers = map[string]bool{
	"steam":            true,
	"steamwebhelper":   true,
	"steamservice":     true,
	"steam.exe":        true,
	"heroic":           true,
	"heroic-wine":      true,
	"lutris":           true,
	"wine":             true,
	"wine64":           true,
	"wine-preloader":   true,
	"wine64-preloader": true,
	"proton":           true,
	"proton_wine":      true,
	"gamescope":        true,
	"gamescope-wsi":    true,
	"mangohud":         true,
}

var (
	pidCache   = map[int]bool{}
	pidCacheMu sync.RWMutex
)

func isGameProcess(pid int) bool {
	if pid <= 0 {
		return false
	}
	pidCacheMu.RLock()
	cached, ok := pidCache[pid]
	pidCacheMu.RUnlock()
	if ok {
		return cached
	}
	originalPid := pid
	for pid > 1 {
		comm, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
		if err != nil {
			break
		}
		name := strings.TrimSpace(string(comm))
		if knownGameLaunchers[name] {
			log.Printf("[gameDetect] PID %d (%s) matches game launcher", pid, name)
			pidCacheMu.Lock()
			pidCache[originalPid] = true
			pidCacheMu.Unlock()
			return true
		}

		env, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
		if err == nil {
			envStr := string(env)
			if strings.Contains(envStr, "SteamAppId\x00") ||
				strings.Contains(envStr, "STEAM_COMPAT_DATA_PATH\x00") ||
				strings.Contains(envStr, "WINEPREFIX\x00") ||
				strings.Contains(envStr, "SteamGameId\x00") {
				log.Printf("[gameDetect] PID %d has game env vars", pid)
				pidCacheMu.Lock()
				pidCache[originalPid] = true
				pidCacheMu.Unlock()
				return true
			}
		}

		ppid := readPPid(pid)
		if ppid == 0 || ppid == pid {
			break
		}
		pid = ppid
	}
	pidCacheMu.Lock()
	pidCache[originalPid] = false
	pidCacheMu.Unlock()
	return false
}

func readPPid(pid int) int {
	status, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				ppid, err := strconv.Atoi(fields[1])
				if err == nil {
					return ppid
				}
			}
		}
	}
	return 0
}

func getFocusedPID() int {
	if isGnome {
		pid, err := gnomeGetFocusPID()
		if err == nil && pid > 0 {
			return pid
		}
		log.Printf("[getFocusedPID] gnome failed: %v", err)
	}
	pid := x11GetFocusPID()
	if pid > 0 {
		log.Printf("[getFocusedPID] x11: %d", pid)
		return pid
	}
	return 0
}
