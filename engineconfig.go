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
			e.macroTable.Reload(e.engineName, autoCap)
		case !on && wasOn:
			e.macroTable.Disable()
		case on:
			e.macroTable.SetAutoCapitalize(e.engineName, autoCap)
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

// persistConfigChangeLocked writes the fields that changed in memory since
// before to the engine's own file, on top of what is on disk now, so changes
// the settings GUI made meanwhile survive and an EN engine never writes the
// per-app mapping it merged in from the VI config.
func (e *Engine) persistConfigChangeLocked(before *config.Config) {
	cur := e.config
	pending := configFilesMtime(e.engineName).After(e.configMtime_)
	config.UpdateConfig(e.engineName, func(c *config.Config) {
		mergeBits := func(disk, old, now uint) uint {
			changed := old ^ now
			return disk&^changed | now&changed
		}
		c.Flags = mergeBits(c.Flags, before.Flags, cur.Flags)
		c.IBflags = mergeBits(c.IBflags, before.IBflags, cur.IBflags)
		if cur.DefaultInputMode != before.DefaultInputMode {
			c.DefaultInputMode = cur.DefaultInputMode
		}
		if cur.OutputCharset != before.OutputCharset {
			c.OutputCharset = cur.OutputCharset
		}
		if cur.InputMethod != before.InputMethod {
			c.InputMethod = cur.InputMethod
		}
	})
	e.syncConfigMtimeLocked(pending)
}

// syncConfigMtimeLocked records the files' mtime after our own write, unless
// another writer had changed them before: then the next FocusIn reloads.
func (e *Engine) syncConfigMtimeLocked(pending bool) {
	if !pending {
		e.configMtime_ = configFilesMtime(e.engineName)
	}
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
	e.propsShown_ = e.propList
	focusPropsOwner.Store(e)
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
	key := inputModeKey{true, e.getWmClass(), e.isGameNow(), atomic.LoadUint64(&e.configGen_)}
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
