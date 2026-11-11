package main

import (
	"encoding/json"
	"testing"
	"time"

	"ktelex/config"
)

// Tests for the echo-aware transaction pipeline in engine_uinput.go.

func resetEchoProfile(wm string) {
	uinputEchoMu.Lock()
	delete(uinputEchoProfiles, echoProfileKey(wm))
	uinputEchoMu.Unlock()
}

func txOpen(e *Engine) (bool, int) {
	e.Lock()
	defer e.Unlock()
	return e.uinputDeleting_, e.uinputExpectingBs_
}

// An app known to echo must NOT get its commit on the ~30ms fallback: the
// echo can be late under load, and a commit that overtakes the queued BS
// is erased by it (lost first Unicode char).
func TestUinputKnownEchoAppWaitsForEcho(t *testing.T) {
	const wm = "kitty:kitty"
	resetEchoProfile(wm)
	defer resetEchoProfile(wm)
	uinputNoteEcho(wm, 0)

	a := newAppModel(t)
	a.e.wmClasses = wm
	a.typeKey('a')
	a.typeKey('s')
	time.Sleep(120 * time.Millisecond)
	if del, _ := txOpen(a.e); !del {
		t.Fatalf("known-echo app: tx committed before its echo arrived (commit=%q)", a.fe.commitText)
	}
	a.typeKey(IBusBackSpace) // late echo
	a.awaitIdle(time.Second)
	if got := a.text(); got != "á" {
		t.Errorf("app buffer=%q, want %q", got, "á")
	}
}

// An app that has been seen never to echo uses the short timer.
func TestUinputNeverEchoAppShortTimer(t *testing.T) {
	const wm = "noecho-app"
	resetEchoProfile(wm)
	defer resetEchoProfile(wm)
	for i := 0; i < kUinputNoEchoStrikes; i++ {
		uinputNoteMissedEcho(wm)
	}
	if uinputEchoState(wm) != echoNever {
		t.Fatalf("profile not classed never-echo")
	}
	a := newAppModel(t)
	a.e.wmClasses = wm
	start := time.Now()
	a.typeKey('a')
	a.typeKey('s')
	a.awaitIdle(time.Second)
	if d := time.Since(start); d > 120*time.Millisecond {
		t.Errorf("never-echo commit took %v, want short timer", d)
	}
}

// Unknown apps learn: zero-echo transactions accumulate strikes.
func TestUinputEchoProfileLearns(t *testing.T) {
	const wm = "learn-app"
	resetEchoProfile(wm)
	defer resetEchoProfile(wm)
	a := newAppModel(t)
	a.e.wmClasses = wm
	for _, w := range []string{"as", " as"} {
		for _, k := range w {
			a.typeKey(uint32(k))
		}
		a.awaitIdle(time.Second)
	}
	if st := uinputEchoState(wm); st != echoNever {
		t.Errorf("after 2 zero-echo tx: state=%d, want never", st)
	}
	uinputNoteEcho(wm, 0)
	if st := uinputEchoState(wm); st != echoSeen {
		t.Errorf("after an echo: state=%d, want seen", st)
	}
}

// Chromium-family apps replace a whole syllable via Shift+Left selection,
// so the field never becomes empty; partial replacements still use BS.
func TestUinputSelectStrategy(t *testing.T) {
	var calls []int
	uinputSelectLeftHook = func(n int) bool { calls = append(calls, n); return true }
	defer func() { uinputSelectLeftHook = nil }()

	a := newAppModel(t)
	a.e.wmClasses = "google-chrome:Google-chrome"
	a.typeKey('a')
	a.typeKey('s')
	a.e.Lock()
	sel := a.e.uinputTxSelect_
	a.e.Unlock()
	// Browser: the inline-completion guard commits " " first, so the
	// selection covers that space plus "a".
	if !sel || len(calls) != 1 || calls[0] != 2 {
		t.Fatalf("whole-syllable replace: select=%v calls=%v, want select of 2", sel, calls)
	}
	// The echo of a selection is Left, not BackSpace; it is forwarded.
	if ret, _ := a.e.uinputProcessKeyEvent(IBusLeft, 0, IBusShiftMask); ret {
		t.Errorf("Left echo was swallowed, want forwarded")
	}
	pollCommitText(t, a.fe, " á", 500*time.Millisecond)

	// "t i e e": pfx("ti")>0 -> Backspace path, no selection.
	b := newAppModel(t)
	b.e.wmClasses = "google-chrome:Google-chrome"
	calls = nil
	for _, k := range "tiee" {
		b.typeKey(uint32(k))
	}
	b.e.Lock()
	sel = b.e.uinputTxSelect_
	b.e.Unlock()
	if sel || len(calls) != 0 {
		t.Errorf("partial replace: select=%v calls=%v, want Backspace", sel, calls)
	}
}

// Without the in-process device the select strategy falls back to BS.
func TestUinputSelectFallsBackToBackspace(t *testing.T) {
	uinputSelectLeftHook = func(int) bool { return false }
	defer func() { uinputSelectLeftHook = nil }()
	a := newAppModel(t)
	a.e.wmClasses = "chromium-browser:Chromium-browser"
	a.typeKey('a')
	a.typeKey('s')
	a.e.Lock()
	sel := a.e.uinputTxSelect_
	a.e.Unlock()
	if sel {
		t.Errorf("select still active after device failure")
	}
}

// A FocusIn flap inside the same app keeps the tx open (the old code ended
// it, committing before the queued deletion landed).
func TestUinputSameAppFocusInKeepsTx(t *testing.T) {
	const wm = "kitty:kitty"
	resetEchoProfile(wm)
	defer resetEchoProfile(wm)
	uinputNoteEcho(wm, 0)

	a := newAppModel(t)
	a.e.wmClasses = wm
	a.typeKey('a')
	a.typeKey('s')
	a.e.uinputOnFocusIn(wm)
	if del, _ := txOpen(a.e); !del {
		t.Fatalf("same-app FocusIn ended the tx")
	}
	a.typeKey(IBusBackSpace)
	a.awaitIdle(time.Second)
	if got := a.text(); got != "á" {
		t.Errorf("app buffer=%q, want %q", got, "á")
	}
}

// FocusIn from another app ends the tx.
func TestUinputOtherAppFocusInEndsTx(t *testing.T) {
	a := newAppModel(t)
	a.e.wmClasses = "kitty:kitty"
	a.typeKey('a')
	a.typeKey('s')
	a.e.uinputOnFocusIn("firefox:firefox")
	if del, _ := txOpen(a.e); del {
		t.Errorf("app-change FocusIn left the tx open")
	}
}

// A commit due while the input is unfocused is held until FocusIn.
func TestUinputCommitHeldWhileUnfocused(t *testing.T) {
	a := newAppModel(t)
	a.typeKey('a')
	a.typeKey('s')
	a.e.uinputOnFocusOut()
	time.Sleep(80 * time.Millisecond) // short watchdog (~30ms) has fired
	a.fe.mu.Lock()
	ct := a.fe.commitText
	a.fe.mu.Unlock()
	if ct != "" {
		t.Fatalf("commit %q sent while unfocused", ct)
	}
	a.e.uinputOnFocusIn("")
	pollCommitText(t, a.fe, "á", 200*time.Millisecond)
}

// A FocusIn long after the last keystroke drops the stale syllable, so the
// next key cannot delete text in the newly focused field.
func TestUinputFocusInDropsStaleSyllable(t *testing.T) {
	a := newAppModel(t)
	a.typeKey('t')
	a.typeKey('i')
	a.e.Lock()
	a.e.uinputLastKeyAt_ = time.Now().Add(-2 * kUinputFocusResetIdle)
	a.e.Unlock()
	a.e.uinputOnFocusIn("")
	if got := a.e.uinputPreeditString(); got != "" {
		t.Errorf("stale syllable kept: %q", got)
	}
}

func TestUinputSelectAppsConfigJSON(t *testing.T) {
	var c config.Config
	if err := json.Unmarshal([]byte(`{"defaultInputMode":0}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.UinputSelectApps != nil {
		t.Errorf("absent key: got %v, want nil (defaults)", c.UinputSelectApps)
	}
	if err := json.Unmarshal([]byte(`{"uinputSelectApps":[]}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.UinputSelectApps == nil || len(c.UinputSelectApps) != 0 {
		t.Errorf("empty list: got %#v, want empty (disabled)", c.UinputSelectApps)
	}
	if err := json.Unmarshal([]byte(`{"uinputSelectApps":["foo"]}`), &c); err != nil {
		t.Fatal(err)
	}
	e, _ := newUinputEchoEngine(t)
	e.config.UinputSelectApps = c.UinputSelectApps
	if !e.uinputUseSelect("foo:Foo") || e.uinputUseSelect("google-chrome") {
		t.Errorf("custom list not honoured")
	}
	e.config.UinputSelectApps = []string{"chrome-*"}
	if !e.uinputUseSelect("chrome-abcdef:Chrome-abcdef") {
		t.Errorf("prefix pattern not honoured")
	}
	out, err := json.Marshal(&config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	_ = json.Unmarshal(out, &back)
	if l, ok := back["uinputSelectApps"].([]any); !ok || len(l) != 1 || l[0] != config.UinputSelectDefaultToken {
		t.Errorf("marshal of nil list should write the default token, got %v", back["uinputSelectApps"])
	}
}
