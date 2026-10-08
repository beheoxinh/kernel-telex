package main

import (
	"testing"
	"time"
)

// App-behavior matrix for the uinput tx pipeline (T11).
//
// A live desktop with keystroke injection is unavailable in CI/headless
// sessions, so this matrix simulates the three app echo profiles at the
// IBus protocol level — the exact boundary where Facebook/WhatsApp
// composers (Chromium Wayland), Firefox/GTK, and terminals differ:
//
//   - perfect: every evdev BS loops back as an IBus BS echo
//     (terminal, GTK editor, Firefox textarea).
//   - zero: the compositor swallows synthetic BS, no echo ever arrives
//     (Chromium Wayland contenteditable: FB/WhatsApp composers).
//   - partial: echoes merged/delayed, only a subset arrives in budget.
//
// The app model tracks what the application itself would display:
// forwarded raw keys insert, forwarded BS echoes delete, CommitText
// inserts the replacement. Assertions target the app-visible buffer,
// not engine internals.

// appModel simulates one application's visible text buffer plus the raw
// keystrokes it received through the IBus forward path.
type appModel struct {
	t    *testing.T
	e    *Engine
	fe   *fakeEngine
	buf  []rune
	seen string // committed text already folded into buf
}

func newAppModel(t *testing.T) *appModel {
	t.Helper()
	e, fe := newUinputEchoEngine(t)
	return &appModel{t: t, e: e, fe: fe}
}

// typeKey feeds one keystroke; ret=false means the app receives it.
func (a *appModel) typeKey(keyval uint32) bool {
	a.t.Helper()
	ret, _ := a.e.uinputProcessKeyEvent(keyval, keyval, 0)
	if !ret {
		if keyval == IBusBackSpace {
			if len(a.buf) > 0 {
				a.buf = a.buf[:len(a.buf)-1]
			}
		} else if r := rune(keyval); r >= 0x20 && r < 0x7f {
			a.buf = append(a.buf, r)
		}
	}
	a.foldCommits()
	return ret
}

// foldCommits appends any newly committed delta to the visible buffer.
func (a *appModel) foldCommits() {
	a.t.Helper()
	a.fe.mu.Lock()
	ct := a.fe.commitText
	a.fe.mu.Unlock()
	if len(ct) > len(a.seen) {
		a.buf = append(a.buf, []rune(ct[len(a.seen):])...)
		a.seen = ct
	}
}

// awaitIdle waits until no tx is open (or the deadline passes).
func (a *appModel) awaitIdle(timeout time.Duration) {
	a.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		a.e.Lock()
		del := a.e.uinputDeleting_
		a.e.Unlock()
		if !del {
			a.foldCommits()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.t.Fatalf("tx still open after %v", timeout)
}

func (a *appModel) text() string {
	a.foldCommits()
	return string(a.buf)
}

// drivePerfectEcho types a word with every BS echo delivered (terminal/GTK).
func (a *appModel) drivePerfectEcho(word string) {
	a.t.Helper()
	for _, k := range word {
		a.typeKey(uint32(k))
		for {
			a.e.Lock()
			del, exp := a.e.uinputDeleting_, a.e.uinputExpectingBs_
			a.e.Unlock()
			if !del {
				break
			}
			for i := 0; i < exp; i++ {
				a.typeKey(IBusBackSpace)
			}
			a.awaitIdle(time.Second)
		}
	}
}

// TestUinputAppMatrixPerfectEcho: terminal/GTK/Firefox profile — full echo,
// app-visible buffer must equal the expected Vietnamese word exactly.
func TestUinputAppMatrixPerfectEcho(t *testing.T) {
	for word, want := range map[string]string{
		"as":      "á",
		"anw":     "ăn",
		"ddaay":   "đây",
		"maanf":   "mần",
		"tieengs": "tiếng",
	} {
		a := newAppModel(t)
		a.drivePerfectEcho(word)
		if got := a.text(); got != want {
			t.Errorf("perfect-echo word %q: app buffer=%q, want %q (commits=%q)",
				word, got, want, a.seen)
		}
	}
}

// TestUinputAppMatrixZeroEcho: Chromium contenteditable profile — no echo
// ever arrives (FB/WhatsApp). The independent tx timer must commit on a
// bounded delay; the tx must never stall waiting for a next keystroke.
func TestUinputAppMatrixZeroEcho(t *testing.T) {
	a := newAppModel(t)
	start := time.Now()
	for _, k := range "tieengs" {
		a.typeKey(uint32(k))
	}
	a.awaitIdle(time.Second)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("zero-echo word committed only after %v, want bounded (<1s)", elapsed)
	}
	a.fe.mu.Lock()
	got := a.fe.commitText
	a.fe.mu.Unlock()
	if got == "" {
		t.Fatalf("zero-echo: no commit landed without echoes")
	}
	a.e.Lock()
	del := a.e.uinputDeleting_
	a.e.Unlock()
	if del {
		t.Errorf("zero-echo: tx still open after timer commit")
	}
}

// TestUinputAppMatrixPartialEcho: merged/delayed echo profile — only a
// subset of echoes arrives. Commit must still land bounded via the timer.
func TestUinputAppMatrixPartialEcho(t *testing.T) {
	a := newAppModel(t)
	for _, k := range "anw" {
		a.typeKey(uint32(k))
	}
	a.e.Lock()
	del, exp := a.e.uinputDeleting_, a.e.uinputExpectingBs_
	a.e.Unlock()
	if !del || exp != 2 {
		t.Fatalf("expected open 2-BS tx, deleting=%v expecting=%d", del, exp)
	}
	a.typeKey(IBusBackSpace) // 1 of 2 echoes
	a.awaitIdle(time.Second)
	a.fe.mu.Lock()
	got := a.fe.commitText
	a.fe.mu.Unlock()
	if got != "ăn" {
		t.Errorf("partial-echo: commit=%q, want %q", got, "ăn")
	}
}

// TestUinputAppMatrixLateStray: a duplicate BS echo arriving after commit
// (inside the guard window) must be swallowed, never forwarded as a user
// backspace that would delete the committed text.
func TestUinputAppMatrixLateStray(t *testing.T) {
	a := newAppModel(t)
	a.drivePerfectEcho("as")
	before := a.text()
	if ret := a.typeKey(IBusBackSpace); !ret {
		t.Fatalf("late stray echo: expected swallow (true), got forward (false)")
	}
	if got := a.text(); got != before {
		t.Errorf("late stray echo changed app buffer %q -> %q", before, got)
	}
}

// TestUinputAppMatrixGuardExpiry: after the 150ms stray guard expires, a BS
// is a genuine user keystroke again and must be forwarded (never swallowed).
func TestUinputAppMatrixGuardExpiry(t *testing.T) {
	a := newAppModel(t)
	a.drivePerfectEcho("as")
	time.Sleep(250 * time.Millisecond)
	if ret := a.typeKey(IBusBackSpace); ret {
		t.Fatalf("post-guard BS: expected user-key forward (false), got swallow (true)")
	}
	if got := a.text(); got != "" {
		t.Errorf("post-guard user BS: app buffer=%q, want %q (á deleted)", got, "")
	}
}

// TestUinputAppMatrixDeferredReplay: a key typed mid-flight is buffered
// (consumed, never lost) and replayed via ForwardKeyEvent after tx end.
func TestUinputAppMatrixDeferredReplay(t *testing.T) {
	a := newAppModel(t)
	a.typeKey('a')
	if !a.typeKey('s') {
		t.Fatalf("expected transform consume for 's'")
	}
	if ret := a.typeKey('x'); !ret {
		t.Fatalf("mid-flight key: expected buffer-consume (true), got forward (false)")
	}
	a.e.Lock()
	nbuf := len(a.e.uinputDeferredKeys_)
	a.e.Unlock()
	if nbuf != 1 {
		t.Fatalf("mid-flight key: buffered=%d, want 1", nbuf)
	}
	a.typeKey(IBusBackSpace)
	a.awaitIdle(time.Second)
	a.fe.mu.Lock()
	fw := a.fe.forwardKeyEvent
	ct := a.fe.commitText
	a.fe.mu.Unlock()
	if ct != "á" {
		t.Errorf("deferred replay: commit=%q, want %q", ct, "á")
	}
	if fw[0] != 'x' || fw[2] != IBusReleaseMask {
		t.Errorf("deferred replay: last ForwardKeyEvent=%v, want release of 'x'", fw)
	}
}

// TestUinputAppMatrixFocusOut: a pending tx flushes (commit + replay) on
// focus loss instead of hanging with a half-open transaction.
func TestUinputAppMatrixFocusOut(t *testing.T) {
	a := newAppModel(t)
	a.typeKey('a')
	a.typeKey('s')
	a.e.FocusOut()
	pollCommitText(t, a.fe, "á", 500*time.Millisecond)
	a.e.Lock()
	del := a.e.uinputDeleting_
	a.e.Unlock()
	if del {
		t.Errorf("FocusOut: tx still open after flush")
	}
}

// TestUinputAppMatrixWmChange: switching apps with an open tx ends it
// through the single tx-end path; the next key starts clean.
func TestUinputAppMatrixWmChange(t *testing.T) {
	a := newAppModel(t)
	a.typeKey('a')
	a.typeKey('s')
	a.e.Lock()
	a.e.uinputLastWm_ = "chrome"
	a.e.Unlock()
	a.e.wmClasses = "firefox"
	a.typeKey('q')
	pollCommitText(t, a.fe, "á", 500*time.Millisecond)
	a.e.Lock()
	del := a.e.uinputDeleting_
	a.e.Unlock()
	if del {
		t.Errorf("wm-change: tx still open after switch")
	}
}
