package main

import (
	"testing"
	"time"
)

// A search box / LibreOffice-style inline completion seen through
// surrounding text: the syllable is kept and the next transform clears the
// completion first, even in a field that is not purpose URL.
func TestInlineCompletionFromSurroundingText(t *testing.T) {
	o := &omniboxModel{a: newAppModel(t)}
	e := o.a.e
	o.key('v')
	o.key('i')
	o.key('e')
	// The app shows "vie[tnamnet.vn]" with the completion selected; the
	// update comes right after the key (not idle).
	e.uinputOnSurroundingText("vietnamnet.vn", 13, 3)
	if e.getRawKeyLen() == 0 {
		t.Fatalf("completion taken for a caret move: syllable dropped")
	}
	o.typeWord("es")
	if got := string(o.a.buf); got != "viế" {
		t.Fatalf("got %q, want %q", got, "viế")
	}
	// A new field starts without the workaround.
	e.uinputOnFocusIn("")
	if e.uinputInlineCompletion_ {
		t.Fatalf("completion flag survived FocusIn")
	}
}

// A real selection elsewhere still drops the syllable.
func TestSelectionAwayFromSyllableStillDrops(t *testing.T) {
	a := newAppModel(t)
	a.typeKey('t')
	idle(a.e)
	a.e.uinputOnSurroundingText("abc t", 0, 3)
	if a.e.getRawKeyLen() != 0 || a.e.uinputInlineCompletion_ {
		t.Fatalf("selection away from the syllable not treated as a caret move")
	}
}

// Switching engine in the middle of a transaction commits the replacement.
func TestDisableCommitsOpenTx(t *testing.T) {
	a := newAppModel(t)
	a.typeKey('a')
	a.typeKey('s') // tx opens: delete "a", commit "á" later
	a.e.Lock()
	open := a.e.uinputDeleting_
	a.e.Unlock()
	if !open {
		t.Fatalf("no tx opened")
	}
	a.e.Disable()
	committed := func() string {
		a.fe.mu.Lock()
		defer a.fe.mu.Unlock()
		return a.fe.commitText
	}
	if c := committed(); c != "á" {
		t.Fatalf("Disable mid-tx committed %q, want á", c)
	}
	time.Sleep(200 * time.Millisecond) // the old watchdog must not commit again
	if c := committed(); c != "á" {
		t.Fatalf("double commit: %q", c)
	}
}

// Windows without WM_CLASS (KDE) still learn that echoes come back.
func TestEchoProfileWithoutWmClass(t *testing.T) {
	resetEchoProfile("")
	defer resetEchoProfile("")
	if uinputEchoState("") != echoUnknown {
		t.Fatalf("fresh state not unknown")
	}
	uinputNoteEcho("", 5*time.Millisecond)
	if uinputEchoState("") != echoSeen {
		t.Fatalf("echo for an unnamed window not learned")
	}
}
