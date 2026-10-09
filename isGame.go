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

var knownGamePatterns = []string{
	"steam_app_",
	"gamescope",
	"wine",
	"proton",
	"lutris",
	"heroic",
	"dota2",
	"cs2",
	"csgo",
	"hl2",
	"tf2",
	"retroarch",
	"dosbox",
	"yuzu",
	"ryujinx",
	"rpcs3",
	"dolphin-emu",
	"pcsx2",
	"cemu",
}

func isGameByWmClass(wmClass string) bool {
	if wmClass == "" {
		return false
	}
	norm := strings.ToLower(wmClass)
	for _, p := range knownGamePatterns {
		if strings.Contains(norm, p) {
			log.Printf("[gameDetect] wmClass %q matches game pattern %q", wmClass, p)
			return true
		}
	}
	return false
}

func findPidByWmClass(wmClass string) int {
	if wmClass == "" {
		return 0
	}
	norm := strings.ToLower(wmClass)
	parts := strings.Split(norm, ":")
	target := parts[len(parts)-1]
	if target == "" {
		return 0
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err == nil {
			c := strings.ToLower(strings.TrimSpace(string(comm)))
			if c == target || strings.HasPrefix(target, c) || strings.HasPrefix(c, target) {
				return pid
			}
		}
	}
	return 0
}

func getFocusedPID(wmClass string) int {
	if isGnome {
		pid, err := gnomeGetFocusPID()
		if err == nil && pid > 0 {
			return pid
		}
	}
	pid := x11GetFocusPID()
	if pid > 0 {
		return pid
	}
	if wmClass != "" {
		if p := findPidByWmClass(wmClass); p > 0 {
			return p
		}
	}
	return 0
}
