package main

import (
	"testing"
	"time"

	"github.com/BambooEngine/bamboo-core"
	"ktelex/config"
)

// Echo-matrix for the uinput BS/commit transaction (T4, red before fix).
//
// Drives Engine.uinputProcessKeyEvent directly:
//   - "as" -> "á" yields deleteLen=1 (one evdev BS in flight).
//   - "uow" -> "ươ" yields deleteLen=2 (two evdev BS in flight).
// uinputBackspace is a no-op without a device, so tests inject the
// compositor BS echoes by feeding IBusBackSpace presses.

func newUinputEchoEngine(t *testing.T) (*Engine, *fakeEngine) {
	t.Helper()
	fe := NewFakeEngine()
	engineName := "test-uinput"
	cfg := config.DefaultCfg(engineName)
	cfg.DefaultInputMode = config.UinputIM
	inputMethod := bamboo.ParseInputMethod(cfg.InputMethodDefinitions, cfg.InputMethod)
	e := NewIbusBambooEngine(engineName, &cfg, fe, bamboo.NewEngine(inputMethod, cfg.Flags))
	return e, fe
}

func feedUinputKey(t *testing.T, e *Engine, keyval uint32) bool {
	t.Helper()
	ret, _ := e.uinputProcessKeyEvent(keyval, keyval, 0)
	return ret
}

// startSingleBsTx types "as" -> "á": 'a' forwards raw, 's' is consumed and
// arms a 1-BS transaction.
func startSingleBsTx(t *testing.T, e *Engine) {
	t.Helper()
	if feedUinputKey(t, e, 'a') {
		t.Fatalf("key 'a': expected raw forward (false), got consume")
	}
	if !feedUinputKey(t, e, 's') {
		t.Fatalf("key 's': expected transform consume (true), got forward")
	}
	e.Lock()
	deleting := e.uinputDeleting_
	expecting := e.uinputExpectingBs_
	e.Unlock()
	if !deleting {
		t.Fatalf("expected uinputDeleting_=true after transform")
	}
	if expecting != 1 {
		t.Fatalf("expected 1 BS in flight, got %d", expecting)
	}
}

// startDoubleBsTx types "anw" -> "ăn": arms a 2-BS transaction.
func startDoubleBsTx(t *testing.T, e *Engine) {
	t.Helper()
	if feedUinputKey(t, e, 'a') {
		t.Fatalf("key 'a': expected raw forward (false), got consume")
	}
	if feedUinputKey(t, e, 'n') {
		t.Fatalf("key 'n': expected raw forward (false), got consume")
	}
	if !feedUinputKey(t, e, 'w') {
		t.Fatalf("key 'w': expected transform consume (true), got forward")
	}
	e.Lock()
	deleting := e.uinputDeleting_
	expecting := e.uinputExpectingBs_
	e.Unlock()
	if !deleting {
		t.Fatalf("expected uinputDeleting_=true after transform")
	}
	if expecting != 2 {
		t.Fatalf("expected 2 BS in flight, got %d", expecting)
	}
}

func pollCommitText(t *testing.T, fe *fakeEngine, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		fe.mu.Lock()
		got := fe.commitText
		fe.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	fe.mu.Lock()
	got := fe.commitText
	fe.mu.Unlock()
	t.Fatalf("commitText=%q, want %q within %v", got, want, timeout)
}

func TestUinputEchoMatrix(t *testing.T) {
	t.Run("all", func(t *testing.T) {
		e, fe := newUinputEchoEngine(t)
		startSingleBsTx(t, e)
		feedUinputKey(t, e, IBusBackSpace)
		pollCommitText(t, fe, "á", 500*time.Millisecond)
	})

	t.Run("zero", func(t *testing.T) {
		e, fe := newUinputEchoEngine(t)
		startSingleBsTx(t, e)
		// Chromium/Facebook swallows the synthetic BS: no echo ever
		// arrives. The commit must still land on a bounded timer.
		pollCommitText(t, fe, "á", 200*time.Millisecond)
	})

	t.Run("partial", func(t *testing.T) {
		e, fe := newUinputEchoEngine(t)
		startDoubleBsTx(t, e)
		// Only 1 of 2 echoes arrives; the tx must not stall forever.
		feedUinputKey(t, e, IBusBackSpace)
		pollCommitText(t, fe, "ăn", 200*time.Millisecond)
	})

	t.Run("late_stray", func(t *testing.T) {
		e, fe := newUinputEchoEngine(t)
		startSingleBsTx(t, e)
		feedUinputKey(t, e, IBusBackSpace)
		pollCommitText(t, fe, "á", 500*time.Millisecond)
		time.Sleep(50 * time.Millisecond)
		// A duplicate BS echo arriving after commit must be swallowed,
		// never forwarded as a user backspace (would delete "á").
		if ret := feedUinputKey(t, e, IBusBackSpace); !ret {
			t.Fatalf("late BS echo: expected swallow (true), got forward (false)")
		}
		fe.mu.Lock()
		got := fe.commitText
		fe.mu.Unlock()
		if got != "á" {
			t.Fatalf("commitText=%q after stray echo, want %q", got, "á")
		}
	})

	t.Run("echo_debt_swallow", func(t *testing.T) {
		e, fe := newUinputEchoEngine(t)
		startDoubleBsTx(t, e)
		// Simulate timer fire with only 1 echo received (debt = 1)
		feedUinputKey(t, e, IBusBackSpace)
		pollCommitText(t, fe, "ăn", 200*time.Millisecond)
		e.Lock()
		owed := e.uinputOwedBs_
		e.Unlock()
		if owed != 1 {
			t.Fatalf("expected owed debt=1, got %d", owed)
		}
		// Sleep past the 80ms stray guard to prove the debt counter is what protects it
		time.Sleep(100 * time.Millisecond)
		// Late arrival of the 2nd echo should be swallowed by debt counter
		if ret := feedUinputKey(t, e, IBusBackSpace); !ret {
			t.Fatalf("late 2nd BS echo: expected swallow by debt counter (true), got forward (false)")
		}
		e.Lock()
		owedAfter := e.uinputOwedBs_
		e.Unlock()
		if owedAfter != 0 {
			t.Fatalf("expected debt=0 after swallowing, got %d", owedAfter)
		}
		fe.mu.Lock()
		got := fe.commitText
		fe.mu.Unlock()
		if got != "ăn" {
			t.Fatalf("commitText=%q corrupted, want %q", got, "ăn")
		}
	})
}
