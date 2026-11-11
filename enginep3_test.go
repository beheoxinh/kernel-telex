package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ktelex/config"

	ibus "github.com/BambooEngine/goibus"
)

// newConfigTestEngine is a uinput test engine whose config file is unique
// to the test (the config dir comes from the passwd entry, not $HOME) and
// removed afterwards.
func newConfigTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, _ := newUinputEchoEngine(t)
	e.engineName = fmt.Sprintf("test-p3-%d", time.Now().UnixNano())
	path := config.GetConfigPath(e.engineName)
	t.Cleanup(func() { os.Remove(path) })
	return e
}

func TestInputModeCacheFollowsConfig(t *testing.T) {
	e, _ := newUinputEchoEngine(t)
	e.wmClasses = "foo:Foo"
	if im := e.getInputMode(); im != config.UinputIM {
		t.Fatalf("default mode %d", im)
	}
	e.config.InputModeMapping = map[string]int{"foo:Foo": config.PreeditIM}
	e.bumpConfigGen()
	if im := e.getInputMode(); im != config.PreeditIM {
		t.Fatalf("mapping not applied after config change: %d", im)
	}
	e.wmClasses = "bar:Bar"
	if im := e.getInputMode(); im != config.UinputIM {
		t.Fatalf("app change not applied: %d", im)
	}
	e.config.IBflags |= config.IBdisableOnGame
	e.bumpConfigGen()
	e.isGame = true
	if im := e.getInputMode(); im != config.UsIM {
		t.Fatalf("game not applied: %d", im)
	}
}

func BenchmarkGetInputMode(b *testing.B) {
	e, _ := newUinputEchoEngine(&testing.T{})
	e.wmClasses = "google-chrome:Google-chrome"
	for i := 0; i < 50; i++ {
		e.config.InputModeMapping[fmt.Sprintf("app-%d", i)] = config.PreeditIM
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.getInputMode()
	}
}

// A config written by another process (GUI, EN engine) is picked up.
func TestReloadConfigIfChanged(t *testing.T) {
	e := newConfigTestEngine(t)
	if e.reloadConfigIfChanged() {
		t.Fatalf("reloaded without a config file")
	}
	cfg := config.DefaultCfg(e.engineName)
	cfg.InputModeMapping = map[string]int{"foo:Foo": config.PreeditIM}
	config.SaveConfig(&cfg, e.engineName)
	e.wmClasses = "foo:Foo"
	e.ProcessKeyEvent('t', 0, 0) // a syllable in progress survives the reload
	if !e.reloadConfigIfChanged() {
		t.Fatalf("new config file not reloaded")
	}
	if im := e.getInputMode(); im != config.PreeditIM {
		t.Fatalf("reloaded mapping not used: %d", im)
	}
	if e.getRawKeyLen() == 0 {
		t.Fatalf("reload dropped the syllable although the input method is unchanged")
	}
	if e.reloadConfigIfChanged() {
		t.Fatalf("reloaded twice for one change")
	}
}

// The settings window must not block the D-Bus handler, and only one opens.
func TestOpenSettingsAsync(t *testing.T) {
	saved := guiCommand
	defer func() { guiCommand = saved }()
	var started int32
	guiCommand = func() *exec.Cmd {
		atomic.AddInt32(&started, 1)
		return exec.Command("sleep", "0.2")
	}
	done := make(chan struct{}, 2)
	t0 := time.Now()
	openSettingsAsync(func() { done <- struct{}{} })
	openSettingsAsync(func() { done <- struct{}{} })
	if d := time.Since(t0); d > 100*time.Millisecond {
		t.Fatalf("openSettingsAsync blocked %v", d)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("done callback not called")
	}
	select {
	case <-done:
		t.Fatalf("second window opened")
	case <-time.After(100 * time.Millisecond):
	}
	if started != 1 {
		t.Fatalf("started %d GUIs", started)
	}
}

// PropertyActivate vs. keys and transaction timers: run with -race.
func TestPropertyActivateConcurrentWithTyping(t *testing.T) {
	e := newConfigTestEngine(t)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			for _, k := range "tieengs " {
				e.ProcessKeyEvent(uint32(k), 0, 0)
			}
		}
	}()
	for i := 0; i < 10; i++ {
		state := uint32(ibus.PROP_STATE_UNCHECKED)
		if i%2 == 0 {
			state = ibus.PROP_STATE_CHECKED
		}
		e.PropertyActivate(PropKeyStdToneStyle, state)
	}
	wg.Wait()
}

// Changing the tone style rebuilds the composer; an unrelated property
// keeps the syllable being typed.
func TestPropertyActivateKeepsSyllable(t *testing.T) {
	e := newConfigTestEngine(t)
	e.ProcessKeyEvent('t', 0, 0)
	e.PropertyActivate("-", ibus.PROP_STATE_CHECKED)
	if e.getRawKeyLen() == 0 {
		t.Fatalf("unrelated property dropped the syllable")
	}
}
