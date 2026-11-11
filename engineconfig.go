package main

import (
	"log"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"ktelex/config"

	"github.com/BambooEngine/bamboo-core"
)

// ── Config reload ───────────────────────────────────────────────────────
//
// The settings GUI runs as a separate process and the EN engine writes the
// shared per-app mapping into the VI config file, so the in-memory config of
// an engine can go stale. Engines reload when the files changed (checked on
// FocusIn, one stat per file).

func configFilesMtime(engineName string) time.Time {
	var t time.Time
	for _, n := range []string{engineName, config.MasterName(engineName)} {
		if st, err := os.Stat(config.GetConfigPath(n)); err == nil && st.ModTime().After(t) {
			t = st.ModTime()
		}
	}
	return t
}

func (e *Engine) bumpConfigGen() { atomic.AddUint64(&e.configGen_, 1) }

// applyConfigLocked installs cfg and rebuilds what depends on it. The
// syllable being typed survives unless the input method or its flags change.
func (e *Engine) applyConfigLocked(cfg *config.Config) {
	old := e.config
	im := bamboo.ParseInputMethod(cfg.InputMethodDefinitions, cfg.InputMethod)
	if im.Name == "" {
		im = bamboo.ParseInputMethod(cfg.InputMethodDefinitions, bamboo.DefaultInputMethodName)
		cfg.InputMethod = bamboo.DefaultInputMethodName
	}
	if e.preeditor == nil || old == nil || old.InputMethod != cfg.InputMethod || old.Flags != cfg.Flags {
		e.preeditor = bamboo.NewEngine(im, cfg.Flags)
	}
	if e.macroTable != nil && old != nil {
		wasOn := old.IBflags&config.IBmacroEnabled != 0
		on := cfg.IBflags&config.IBmacroEnabled != 0
		autoCap := cfg.IBflags&config.IBautoCapitalizeMacro != 0
		switch {
		case on && !wasOn:
			e.macroTable.autoCapitalizeMacro = autoCap
			e.macroTable.Enable(e.engineName)
		case !on && wasOn:
			e.macroTable.Disable()
		case on && autoCap != e.macroTable.autoCapitalizeMacro:
			e.macroTable.autoCapitalizeMacro = autoCap
			e.macroTable.LoadFromFile(config.GetMacroPath(e.engineName))
		}
	}
	if cfg.IBflags&config.IBspellCheckWithDicts != 0 {
		ensureDictionary()
	}
	e.config = cfg
	e.propList = GetPropListByConfig(cfg)
	e.configMtime_ = configFilesMtime(e.engineName)
	e.bumpConfigGen()
}

// reloadConfigIfChanged reloads the config when a file is newer than the
// copy in memory. Returns true when it did.
func (e *Engine) reloadConfigIfChanged() bool {
	mt := configFilesMtime(e.engineName)
	e.Lock()
	defer e.Unlock()
	if mt.IsZero() || !mt.After(e.configMtime_) {
		return false
	}
	if e.uinputTxOpenLocked() {
		return false // never swap config under an open transaction; next FocusIn
	}
	log.Printf("config changed on disk: reloading %s", e.engineName)
	e.applyConfigLocked(config.LoadConfig(e.engineName))
	e.RegisterProperties(e.propList)
	return true
}

// ── Settings GUI ────────────────────────────────────────────────────────

var guiMu sync.Mutex
var guiRunning bool

// guiCommand is hookable for tests.
var guiCommand = func() *exec.Cmd {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	return exec.Command(exe, "--gui")
}

// openSettingsAsync shows the settings window in its own process. It used to
// run GTK inside the IBus handler, blocking every other D-Bus call (keys
// included) of the engine while the window was open. done runs after the
// window closes. A second request while it is open is ignored.
func openSettingsAsync(done func()) {
	guiMu.Lock()
	if guiRunning {
		guiMu.Unlock()
		return
	}
	cmd := guiCommand()
	if cmd == nil {
		guiMu.Unlock()
		return
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		guiMu.Unlock()
		log.Printf("settings: start GUI: %v", err)
		return
	}
	guiRunning = true
	guiMu.Unlock()
	go func() {
		_ = cmd.Wait()
		guiMu.Lock()
		guiRunning = false
		guiMu.Unlock()
		if done != nil {
			done()
		}
	}()
}

// ── Input-mode cache ────────────────────────────────────────────────────

// inputModeKey is everything getInputMode() depends on.
type inputModeKey struct {
	valid bool
	wm    string
	game  bool
	gen   uint64
}

func (e *Engine) getInputMode() int {
	key := inputModeKey{true, e.getWmClass(), e.isGame, atomic.LoadUint64(&e.configGen_)}
	e.imCacheMu.Lock()
	if e.imCache == key {
		im := e.imCacheMode
		e.imCacheMu.Unlock()
		return im
	}
	e.imCacheMu.Unlock()
	im := e.computeInputMode(key.wm)
	e.imCacheMu.Lock()
	e.imCache, e.imCacheMode = key, im
	e.imCacheMu.Unlock()
	return im
}
