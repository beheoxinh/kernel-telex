package main

import (
	"testing"
	"time"
)

// Ctrl/Alt chords (paste, Ctrl+Backspace…) change the field behind the
// engine's back: the next tone key must not delete anything.
func TestChordDropsSyllable(t *testing.T) {
	for _, chord := range []struct {
		key, state uint32
	}{
		{'v', IBusControlMask},           // paste
		{IBusBackSpace, IBusControlMask}, // delete word
		{'w', IBusControlMask},           // terminal: delete word
		{'1', IBusMod1Mask},              // Alt+1
	} {
		a := newAppModel(t)
		a.typeKey('a')
		a.typeKey('n')
		if ret, _ := a.e.uinputProcessKeyEvent(chord.key, chord.key, chord.state); ret {
			t.Fatalf("chord 0x%x consumed", chord.key)
		}
		if got := a.e.uinputPreeditString(); got != "" {
			t.Fatalf("chord 0x%x: syllable %q kept", chord.key, got)
		}
		if a.typeKey('s') {
			t.Errorf("chord 0x%x: 's' after it transformed the old syllable", chord.key)
		}
		if del, _ := txOpen(a.e); del {
			t.Errorf("chord 0x%x: Backspace transaction opened", chord.key)
		}
	}
}

// Without a virtual keyboard the old syllable must still be removed (here
// through DeleteSurroundingText), never left next to the replacement.
func TestNoInjectionDoesNotDuplicate(t *testing.T) {
	saved := uinputBackspaceHook
	uinputBackspaceHook = func(int) bool { return false }
	defer func() { uinputBackspaceHook = saved }()
	a := newAppModel(t)
	deleted := 0
	savedDel := uinputDeleteSurroundingHook
	uinputDeleteSurroundingHook = func(n int) {
		deleted += n
		a.foldCommits()
		a.buf = a.buf[:len(a.buf)-n]
	}
	defer func() { uinputDeleteSurroundingHook = savedDel }()
	a.typeKey('a')
	a.typeKey('n')
	a.typeKey('s')
	if del, _ := txOpen(a.e); del {
		t.Fatalf("transaction left open without injection")
	}
	if got := a.text(); got != "án" {
		t.Fatalf("app=%q want %q (deleted %d)", got, "án", deleted)
	}
}

// A FocusIn from another application with a tx open drops the commit and
// the buffered keys instead of typing them into the new app.
func TestFocusInOtherAppAbortsTx(t *testing.T) {
	a := newAppModel(t)
	a.e.Lock()
	a.e.uinputLastWm_ = "chrome"
	a.e.Unlock()
	a.typeKey('a')
	a.typeKey('s') // tx open, waiting for the Backspace echo
	a.typeKey('x') // buffered
	a.e.uinputOnFocusIn("firefox")
	time.Sleep(30 * time.Millisecond)
	a.fe.mu.Lock()
	got := a.fe.commitText
	a.fe.mu.Unlock()
	if got != "" {
		t.Fatalf("committed %q into the new app", got)
	}
	if del, _ := txOpen(a.e); del {
		t.Fatalf("tx still open")
	}
	if s := a.e.uinputPreeditString(); s != "" {
		t.Fatalf("syllable %q kept across the app switch", s)
	}
}
