package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// gameAncestors: processes that run games. A window whose process
// descends from one of them is a game.
var gameAncestors = map[string]bool{
	"steam":         true,
	"steam.exe":     true,
	"heroic":        true,
	"heroic-wine":   true,
	"lutris":        true,
	"proton":        true,
	"proton_wine":   true,
	"gamescope":     true,
	"gamescope-wsi": true,
	"legendary":     true,
	"gamemoderun":   true,
}

// launcherUIs: the launchers' own windows (Steam chat, store search, the
// Heroic/Lutris library) are ordinary text fields, not games.
var launcherUIs = map[string]bool{
	"steam":          true,
	"steamwebhelper": true,
	"heroic":         true,
	"lutris":         true,
}

// isGameProcess walks pid and its ancestors in /proc.
func isGameProcess(pid int) bool {
	if pid <= 1 {
		return false
	}
	for depth := 0; pid > 1 && depth < 32; depth++ {
		comm, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
		if err != nil {
			break
		}
		name := strings.TrimSpace(string(comm))
		if depth == 0 && launcherUIs[name] {
			return false
		}
		if gameAncestors[name] {
			log.Printf("[gameDetect] PID %d (%s) runs games", pid, name)
			return true
		}
		// Set by Steam for the game it launches (and the game's children).
		if env, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ")); err == nil {
			for _, kv := range strings.Split(string(env), "\x00") {
				k, v, _ := strings.Cut(kv, "=")
				if (k == "SteamAppId" || k == "SteamGameId" || k == "STEAM_COMPAT_APP_ID") && v != "" && v != "0" {
					log.Printf("[gameDetect] PID %d has %s=%s", pid, k, v)
					return true
				}
			}
		}
		ppid := readPPid(pid)
		if ppid == 0 || ppid == pid {
			break
		}
		pid = ppid
	}
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

// knownGameNames are matched against a whole WM_CLASS part (instance or
// class), optionally followed by '-', '_' or '.': "proton" must not catch
// "protonmail", "cs2" must not catch "docs2".
var knownGameNames = []string{
	"gamescope", "dota2", "cs2", "csgo", "hl2", "hl2_linux", "tf2", "retroarch",
	"dosbox", "dosbox-x", "yuzu", "ryujinx", "rpcs3", "dolphin-emu", "pcsx2", "cemu",
	"minecraft", "factorio", "rimworld", "stardew valley",
}

func isGameByWmClass(wmClass string) bool {
	if wmClass == "" {
		return false
	}
	for _, part := range strings.Split(strings.ToLower(wmClass), ":") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "steam_app_") || strings.HasPrefix(part, "steam_proton") {
			log.Printf("[gameDetect] wmClass %q is a Steam game", wmClass)
			return true
		}
		for _, g := range knownGameNames {
			if part == g || (strings.HasPrefix(part, g) && len(part) > len(g) && strings.ContainsRune("-_.", rune(part[len(g)]))) {
				log.Printf("[gameDetect] wmClass %q matches game %q", wmClass, g)
				return true
			}
		}
	}
	return false
}

// findPidByWmClass is the last resort when neither the shell nor X11 can
// name the focused process: a process whose name is the WM_CLASS.
func findPidByWmClass(wmClass string) int {
	if wmClass == "" {
		return 0
	}
	norm := strings.ToLower(wmClass)
	parts := strings.Split(norm, ":")
	target := parts[len(parts)-1]
	if len(target) < 3 {
		return 0
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err != nil {
			continue
		}
		c := strings.ToLower(strings.TrimSpace(string(comm)))
		// comm is cut to 15 bytes: only then is a prefix a match.
		if c == target || (len(c) == 15 && strings.HasPrefix(target, c)) {
			return pid
		}
	}
	return 0
}

type gameVerdict struct {
	game bool
	at   time.Time
	ttl  time.Duration
}

const gameWmCacheMax = 256

var (
	gameWmCache   = map[string]gameVerdict{}
	gameWmCacheMu sync.Mutex
)

// isGameWindow classifies the focused window, cached per WM_CLASS for a
// while: browsers and Electron apps flap FocusIn many times per second and
// the PID lookup costs D-Bus round trips plus a /proc walk. The cache
// expires (the same class can be a game later, PIDs are reused) and is
// bounded.
func isGameWindow(wmClass string) bool {
	key := strings.ToLower(wmClass)
	now := time.Now()
	gameWmCacheMu.Lock()
	v, ok := gameWmCache[key]
	gameWmCacheMu.Unlock()
	if ok && now.Sub(v.at) < v.ttl {
		return v.game
	}
	game := isGameByWmClass(wmClass)
	ttl := 10 * time.Minute
	if !game {
		pid := getFocusedPID(wmClass)
		game = isGameProcess(pid)
		if pid <= 0 {
			ttl = 30 * time.Second // unknown process: ask again soon
		}
		debugf("[gameDetect] %s (pid %d) game=%v", wmClass, pid, game)
	}
	gameWmCacheMu.Lock()
	if len(gameWmCache) >= gameWmCacheMax {
		for k, old := range gameWmCache {
			if now.Sub(old.at) >= old.ttl {
				delete(gameWmCache, k)
			}
		}
		if len(gameWmCache) >= gameWmCacheMax {
			gameWmCache = map[string]gameVerdict{}
		}
	}
	gameWmCache[key] = gameVerdict{game, now, ttl}
	gameWmCacheMu.Unlock()
	return game
}

func getFocusedPID(wmClass string) int {
	if isGnome {
		pid, err := gnomeGetFocusPID()
		if err == nil && pid > 0 {
			return pid
		}
	}
	if !(isWayland && isGnome) {
		if pid := x11GetFocusPID(); pid > 0 {
			return pid
		}
	}
	if wmClass != "" {
		if p := findPidByWmClass(wmClass); p > 0 {
			return p
		}
	}
	return 0
}
