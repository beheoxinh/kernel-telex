package main

import (
	"testing"
	"time"
)

// Surrounding-text replace (uinputTryDstLocked): the app reports its text
// after every change, the engine deletes through DeleteSurroundingText and
// no key is injected — Excel/Sheets never see an arrow key or Backspace.

func dstApp(t *testing.T, wm string, honour bool) *appModel {
	t.Helper()
	a := newAppModel(t)
	a.e.wmClasses = wm
	saved := uinputDeleteSurroundingHook
	uinputDeleteSurroundingHook = func(n int) {
		if !honour {
			return
		}
		a.foldCommits()
		if n > len(a.buf) {
			n = len(a.buf)
		}
		a.buf = a.buf[:len(a.buf)-n]
	}
	t.Cleanup(func() {
		uinputDeleteSurroundingHook = saved
		uinputEchoMu.Lock()
		delete(uinputDstBroken, echoProfileKey(wm))
		delete(uinputDstLag, echoProfileKey(wm))
		uinputEchoMu.Unlock()
	})
	a.report()
	return a
}

// report sends the app's text with the caret at the end, as a client does.
func (a *appModel) report() {
	a.foldCommits()
	n := uint32(len(a.buf))
	a.e.uinputOnSurroundingText(string(a.buf), n, n)
}

func TestDstReplaceWithoutKeys(t *testing.T) {
	selects := 0
	savedSel := uinputSelectLeftHook
	uinputSelectLeftHook = func(int) bool { selects++; return true }
	defer func() { uinputSelectLeftHook = savedSel }()

	for word, want := range map[string]string{
		"ans": "án", "anhs": "ánh", "oongf": "ồng", "tieengs": "tiếng", "ddi": "đi", "as": "á",
	} {
		a := dstApp(t, "google-chrome:Google-chrome", true)
		for _, k := range word {
			a.typeKey(uint32(k))
			if del, _ := txOpen(a.e); del {
				t.Fatalf("%q: key transaction opened at %q", word, string(k))
			}
			a.report()
		}
		if got := a.text(); got != want {
			t.Errorf("%q: app=%q want %q", word, got, want)
		}
	}
	if selects != 0 {
		t.Errorf("Shift+Left used %d times", selects)
	}
}

// Text before the caret that does not end with the syllable (the app has
// not reported the last key yet: fast typing) is never trusted. The engine
// waits for the app's update, buffering keys typed meanwhile, then replaces
// through surrounding text and replays them in order.
func TestDstStaleSurroundingWaitsForUpdate(t *testing.T) {
	calls := 0
	a := dstApp(t, "google-chrome:Google-chrome", true)
	inner := uinputDeleteSurroundingHook
	uinputDeleteSurroundingHook = func(n int) { calls++; inner(n) }
	a.typeKey('a')
	a.report()
	a.typeKey('n') // no report yet: the field state is stale
	a.typeKey('s')
	if calls != 0 {
		t.Fatalf("DeleteSurroundingText used on stale text")
	}
	if !a.typeKey('h') {
		t.Fatalf("key typed while waiting was not buffered")
	}
	a.report() // the app catches up: "an"
	if calls != 1 {
		t.Fatalf("DeleteSurroundingText calls=%d, want 1", calls)
	}
	if del, _ := txOpen(a.e); del {
		t.Fatalf("key transaction opened")
	}
	if got := a.text(); got != "ánh" {
		t.Fatalf("app=%q want %q", got, "ánh")
	}
	if got := a.e.uinputPreeditString(); got != "ánh" {
		t.Fatalf("syllable=%q want %q (buffered key replayed)", got, "ánh")
	}
	a.fe.mu.Lock()
	fw := a.fe.forwardKeyEvent
	a.fe.mu.Unlock()
	if fw[0] != 'h' {
		t.Fatalf("buffered 'h' not forwarded after the replace: %v", fw)
	}
}

// No update within kUinputDstAwait: Backspace transaction; an app that keeps
// lagging is not waited for any more.
func TestDstStaleSurroundingTimesOut(t *testing.T) {
	const wm = "dst-laggy-app:Dst-laggy-app"
	for i := 0; i < kUinputDstLagMax; i++ {
		a := dstApp(t, wm, true)
		a.typeKey('a')
		a.report()
		a.typeKey('n')
		a.typeKey('s')
		if del, _ := txOpen(a.e); del {
			t.Fatalf("round %d: Backspace before waiting for the app", i)
		}
		time.Sleep(kUinputDstAwait + 30*time.Millisecond)
		a.e.Lock()
		del, exp := a.e.uinputDeleting_, a.e.uinputExpectingBs_
		a.e.Unlock()
		if !del || exp != 2 {
			t.Fatalf("round %d: tx open=%v expect=%d after timeout, want Backspace x2", i, del, exp)
		}
		a.e.Lock()
		a.e.uinputResetTxStateLocked()
		a.e.Unlock()
	}
	a := dstApp(t, wm, true)
	a.typeKey('a')
	a.report()
	a.typeKey('n')
	a.typeKey('s')
	if del, _ := txOpen(a.e); !del {
		t.Fatalf("laggy app still waited for surrounding text")
	}
}

// An app that ignores DeleteSurroundingText is repaired with Backspace and
// never offered the path again.
func TestDstIgnoredIsRepairedAndLearned(t *testing.T) {
	const wm = "dst-deaf-app:Dst-deaf-app"
	a := dstApp(t, wm, false)
	a.typeKey('a')
	a.report()
	a.typeKey('n')
	a.report()
	a.typeKey('s') // deletion ignored: app shows "anán"
	if got := a.text(); got != "anán" {
		t.Fatalf("model: app=%q", got)
	}
	a.report() // self-check sees the failure
	if uinputDstAllowed(wm) {
		t.Fatalf("app not marked as ignoring DeleteSurroundingText")
	}
	a.e.Lock()
	del, exp := a.e.uinputDeleting_, a.e.uinputExpectingBs_
	a.e.Unlock()
	if !del || exp != 4 {
		t.Fatalf("repair tx: open=%v expect=%d, want 4 Backspaces", del, exp)
	}
	for i := 0; i < exp; i++ {
		a.typeKey(IBusBackSpace)
	}
	a.awaitIdle(time.Second)
	if got := a.text(); got != "án" {
		t.Fatalf("after repair app=%q want %q", got, "án")
	}
	// Next transform goes through keys.
	a.report()
	a.typeKey('f')
	if del, _ := txOpen(a.e); !del {
		t.Errorf("learned app still uses DeleteSurroundingText")
	}
}

// A selection (inline completion) makes the surrounding text unusable.
func TestDstNotUsedWithSelection(t *testing.T) {
	calls := 0
	a := dstApp(t, "google-chrome:Google-chrome", true)
	inner := uinputDeleteSurroundingHook
	uinputDeleteSurroundingHook = func(n int) { calls++; inner(n) }
	a.typeKey('d')
	a.e.uinputOnSurroundingText("dantri.com.vn", 13, 1)
	a.typeKey('d')
	if calls != 0 {
		t.Fatalf("DeleteSurroundingText used with a selection")
	}
}

// Burst typing: every surrounding-text report lags one key behind. The
// result must match slow typing and no Backspace may be injected.
func TestDstBurstWithLaggingReports(t *testing.T) {
	for word, want := range map[string]string{
		"ans": "án", "tieengs": "tiếng", "nguowif": "người", "ddaays": "đấy", "vieetj": "việt",
	} {
		a := dstApp(t, "google-chrome:Google-chrome", true)
		forwarded := 0
		for _, k := range word {
			prev := string(a.buf)
			if !a.typeKey(uint32(k)) {
				forwarded++
			}
			n := uint32(len([]rune(prev)))
			a.e.uinputOnSurroundingText(prev, n, n) // one key late
		}
		a.report()
		if del, _ := txOpen(a.e); del {
			t.Fatalf("%q: Backspace transaction opened", word)
		}
		if got := a.e.uinputPreeditString(); got != want {
			t.Errorf("%q: syllable=%q want %q", word, got, want)
		}
		if a.e.uinputAwait_ != nil {
			t.Errorf("%q: still waiting for surrounding text", word)
		}
	}
}
