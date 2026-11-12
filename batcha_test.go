package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ktelex/config"

	ibus "github.com/BambooEngine/goibus"
)

// countInjections counts Backspaces sent through uinput during the test.
func countInjections(t *testing.T) *int32 {
	t.Helper()
	var n int32
	saved := uinputBackspaceHook
	uinputBackspaceHook = func(k int) bool { atomic.AddInt32(&n, int32(k)); return true }
	t.Cleanup(func() { uinputBackspaceHook = saved })
	return &n
}

// Keys buffered while a replace waits for surrounding text belong to the old
// app: an app switch drops them instead of replaying them into the new one.
func TestAwaitDropOnAppChangeDiscardsKeys(t *testing.T) {
	inj := countInjections(t)
	a := dstApp(t, "google-chrome:Google-chrome", true)
	a.typeKey('a')
	a.report()
	a.typeKey('n')
	a.typeKey('s') // stale field: waits
	if !a.typeKey('x') {
		t.Fatalf("key typed while waiting was not buffered")
	}
	a.e.uinputOnFocusOut()
	a.e.wmClasses = "firefox:Firefox"
	a.e.uinputOnFocusIn("firefox:Firefox")
	a.e.Lock()
	defer a.e.Unlock()
	if a.e.uinputAwait_ != nil || len(a.e.uinputDeferredKeys_) != 0 || a.e.uinputTxOpenLocked() {
		t.Fatalf("await/keys/tx left after app change")
	}
	if got := a.e.uinputPreeditString(); got != "" {
		t.Fatalf("syllable %q survived the app change", got)
	}
	if n := atomic.LoadInt32(inj); n != 0 {
		t.Fatalf("%d Backspaces injected into the new app", n)
	}
}

// A key that arrives after focus left (long after the last key) but before
// FocusIn finished asking GNOME Shell for the new app must not edit the new
// field with the old syllable.
func TestKeyBeforeFocusInDropsSyllable(t *testing.T) {
	inj := countInjections(t)
	a := newAppModel(t)
	a.drivePerfectEcho("an")
	a.e.Lock()
	a.e.uinputLastKeyAt_ = time.Now().Add(-2 * time.Second)
	a.e.Unlock()
	a.e.uinputOnFocusOut()
	before := atomic.LoadInt32(inj)
	if a.typeKey('s') {
		t.Fatalf("tone key consumed: it would rewrite text in the new field")
	}
	if n := atomic.LoadInt32(inj) - before; n != 0 {
		t.Fatalf("%d Backspaces injected", n)
	}
}

// Backspace on an inline completion removes the completion only.
func TestInlineCompletionBackspaceKeepsSyllable(t *testing.T) {
	a := dstApp(t, "google-chrome:Google-chrome", true)
	for _, k := range "vie" {
		a.typeKey(uint32(k))
		a.report()
	}
	a.e.uinputOnSurroundingText("vietnamnet", 3, 10) // "vie[tnamnet]"
	if a.typeKey(IBusBackSpace) {
		t.Fatalf("Backspace consumed")
	}
	a.e.Lock()
	got, flag := a.e.uinputPreeditString(), a.e.uinputInlineCompletion_
	a.e.Unlock()
	if got != "vie" || flag {
		t.Fatalf("after BS on completion: syllable=%q completion=%v, want \"vie\" false", got, flag)
	}
	a.e.uinputOnSurroundingText("vie", 3, 3)
	a.e.Lock()
	defer a.e.Unlock()
	if a.e.uinputInlineCompletion_ {
		t.Fatalf("completion flag survives a report without selection")
	}
}

// The key path deletes in the output charset's code points.
func TestKeyDeleteCountFollowsCharset(t *testing.T) {
	e, _ := newUinputEchoEngine(t)
	if n := e.uinputKeyDel("ấn", 2); n != 2 {
		t.Fatalf("Unicode: %d, want 2", n)
	}
	e.config.OutputCharset = "Unicode tổ hợp"
	if n := e.uinputKeyDel("ấn", 2); n != 3 {
		t.Fatalf("Unicode tổ hợp: %d, want 3", n)
	}
	if n := e.uinputKeyDel("ấn", 1); n != 1 {
		t.Fatalf("Unicode tổ hợp, last char: %d, want 1", n)
	}
}

// A Backspace right after a surrounding-text replace (nothing injected) is
// the user's and must reach the app.
func TestBackspaceAfterDstNotSwallowed(t *testing.T) {
	a := dstApp(t, "google-chrome:Google-chrome", true)
	for _, k := range "as" {
		a.typeKey(uint32(k))
		a.report()
	}
	if a.typeKey(IBusBackSpace) {
		t.Fatalf("user Backspace swallowed right after a surrounding-text replace")
	}
}

// A panic in key handling is recovered and the composer starts fresh.
func TestRecoverKeyPanic(t *testing.T) {
	e, _ := newUinputEchoEngine(t)
	old := e.preeditor
	func() {
		e.Lock()
		defer e.Unlock()
		defer e.recoverKeyPanicLocked("test")
		panic("boom")
	}()
	if e.preeditor == old {
		t.Fatalf("composer not replaced after a panic")
	}
	e.Lock() // the lock was released
	e.Unlock()
}

// Surrounding-text mode: keys (worker goroutine), Reset, FocusOut, menu
// actions and surrounding text arrive concurrently (run with -race).
func TestSurroundModeConcurrent(t *testing.T) {
	e, _ := newUinputEchoEngine(t)
	e.wmClasses = "surround-app:Surround-app"
	e.config.InputModeMapping = map[string]int{"surround-app:Surround-app": config.SurroundingTextIM}
	e.shouldEnqueuKeyStrokes = true
	e.bumpConfigGen()
	if !e.inBackspaceWhiteList() {
		t.Skip("surrounding-text mode not in the backspace list")
	}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			for _, k := range "tieengs vieetj " {
				e.ProcessKeyEvent(uint32(k), 0, 0)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			e.Reset()
			e.FocusOut()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			e.PropertyActivate(PropKeyStdToneStyle, uint32(i%2))
		}
	}()
	wg.Wait()
	waitSurroundIdle()
	if n := atomic.LoadInt32(&surroundInflight); n != 0 {
		t.Fatalf("worker still has %d keys", n)
	}
}

// Preedit mode: keys vs. menu actions and app switches (run with -race).
func TestPreeditModeConcurrentConfigAndFocus(t *testing.T) {
	e, _ := newUinputEchoEngine(t)
	e.wmClasses = "preedit-app:Preedit-app"
	e.config.InputModeMapping = map[string]int{"preedit-app:Preedit-app": config.PreeditIM}
	e.bumpConfigGen()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			for _, k := range "dduwowcj " {
				e.ProcessKeyEvent(uint32(k), 0, 0)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			e.PropertyActivate(PropKeyStdToneStyle, uint32(i%2))
			e.PropertyActivate(PropKeyEnableSpellCheck, ibus.PROP_STATE_CHECKED)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			e.Lock()
			e.checkWmClass([]string{"preedit-app:Preedit-app", "other:Other"}[i%2])
			e.getInputMode()
			e.Unlock()
		}
	}()
	wg.Wait()
}
