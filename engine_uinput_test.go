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

	t.Run("focus_out_preedit_mode_commits_pending", func(t *testing.T) {
		fe := NewFakeEngine()
		engineName := "test-preedit-focusout"
		cfg := config.DefaultCfg(engineName)
		cfg.DefaultInputMode = config.PreeditIM
		inputMethod := bamboo.ParseInputMethod(cfg.InputMethodDefinitions, cfg.InputMethod)
		e := NewIbusBambooEngine(engineName, &cfg, fe, bamboo.NewEngine(inputMethod, cfg.Flags))

		// Type in Preedit mode without space
		e.ProcessKeyEvent('d', 'd', 0)
		e.ProcessKeyEvent('u', 'u', 0)
		if e.getRawKeyLen() == 0 {
			t.Fatalf("expected non-empty preedit buffer")
		}
		fe.mu.Lock()
		preedit := fe.preeditText
		fe.mu.Unlock()
		if preedit == "" {
			t.Fatalf("expected pending preeditText in fake engine")
		}

		// FocusOut should commit preedit to losing window
		e.FocusOut()
		fe.mu.Lock()
		committed := fe.commitText
		fe.mu.Unlock()
		if committed != preedit {
			t.Fatalf("FocusOut commitText=%q, want %q", committed, preedit)
		}
		if e.getRawKeyLen() != 0 {
			t.Fatalf("FocusOut should reset rawKeyLen, got %d", e.getRawKeyLen())
		}
	})

	t.Run("enter_key_fast_path_in_game_and_empty_buffer", func(t *testing.T) {
		fe := NewFakeEngine()
		engineName := "test-enter-fast-path"
		cfg := config.DefaultCfg(engineName)
		cfg.DefaultInputMode = config.UinputIM
		inputMethod := bamboo.ParseInputMethod(cfg.InputMethodDefinitions, cfg.InputMethod)
		e := NewIbusBambooEngine(engineName, &cfg, fe, bamboo.NewEngine(inputMethod, cfg.Flags))

		// When buffer is empty: Return / KP_Enter should fast-path return (false, nil)
		consumed, err := e.ProcessKeyEvent(IBusReturn, IBusReturn, 0)
		if err != nil || consumed {
			t.Fatalf("empty buffer Enter: expected consumed=false, got %v", consumed)
		}
		consumed, err = e.ProcessKeyEvent(IBusKP_Enter, IBusKP_Enter, 0)
		if err != nil || consumed {
			t.Fatalf("empty buffer KP_Enter: expected consumed=false, got %v", consumed)
		}

		// When isGame = true: Return / KP_Enter should fast-path return (false, nil) immediately
		e.isGame = true
		consumed, err = e.ProcessKeyEvent(IBusReturn, IBusReturn, 0)
		if err != nil || consumed {
			t.Fatalf("game mode Enter: expected consumed=false, got %v", consumed)
		}
		consumed, err = e.ProcessKeyEvent(IBusKP_Enter, IBusKP_Enter, 0)
		if err != nil || consumed {
			t.Fatalf("game mode KP_Enter: expected consumed=false, got %v", consumed)
		}
	})

	t.Run("anti_runaway_backspace_burst_suppression", func(t *testing.T) {
		fe := NewFakeEngine()
		engineName := "test-anti-runaway"
		cfg := config.DefaultCfg(engineName)
		cfg.DefaultInputMode = config.UinputIM
		inputMethod := bamboo.ParseInputMethod(cfg.InputMethodDefinitions, cfg.InputMethod)
		e := NewIbusBambooEngine(engineName, &cfg, fe, bamboo.NewEngine(inputMethod, cfg.Flags))

		// Simulate user typing a word without open in-flight tx: "xin"
		e.ProcessKeyEvent('x', 'x', 0)
		e.ProcessKeyEvent('i', 'i', 0)
		e.ProcessKeyEvent('n', 'n', 0)

		initialRawLen := e.getRawKeyLen()
		if initialRawLen != 3 {
			t.Fatalf("expected raw key len 3, got %d", initialRawLen)
		}
		if e.uinputDeleting_ {
			t.Fatalf("expected uinputDeleting_ to be false")
		}

		// First Backspace with normal timing: user backspace -> forwarded (consumed=false)
		e.uinputLastIdleBsAt_ = time.Now().Add(-100 * time.Millisecond)
		consumed, err := e.ProcessKeyEvent(IBusBackSpace, IBusBackSpace, 0)
		if err != nil || consumed {
			t.Fatalf("1st BS should be forwarded to app, got consumed=%v err=%v", consumed, err)
		}

		// Second Backspace after 30ms (< 45ms): burst count = 2 -> still forwarded
		e.uinputLastIdleBsAt_ = time.Now().Add(-30 * time.Millisecond)
		consumed, err = e.ProcessKeyEvent(IBusBackSpace, IBusBackSpace, 0)
		if err != nil || consumed {
			t.Fatalf("2nd rapid BS should be forwarded to app, got consumed=%v", consumed)
		}

		// Third rapid Backspace after 30ms: burst count = 3 >= 3 -> RUNAWAY GUARD TRIGGERS!
		// It MUST be swallowed (consumed=true), NOT forwarded to app, and NOT modifying preeditor
		rawLenBeforeRunaway := e.getRawKeyLen()
		e.uinputLastIdleBsAt_ = time.Now().Add(-30 * time.Millisecond)
		consumed, err = e.ProcessKeyEvent(IBusBackSpace, IBusBackSpace, 0)
		if err != nil {
			t.Fatalf("3rd BS unexpected error: %v", err)
		}
		if !consumed {
			t.Fatalf("3rd rapid BS (runaway) must be consumed/swallowed, got consumed=false")
		}
		if e.getRawKeyLen() != rawLenBeforeRunaway {
			t.Fatalf("runaway BS modified preeditor: rawLen=%d, want=%d", e.getRawKeyLen(), rawLenBeforeRunaway)
		}

		// Fourth rapid Backspace -> continues to be swallowed
		e.uinputLastIdleBsAt_ = time.Now().Add(-30 * time.Millisecond)
		consumed, err = e.ProcessKeyEvent(IBusBackSpace, IBusBackSpace, 0)
		if err != nil || !consumed {
			t.Fatalf("4th rapid BS must be swallowed, got consumed=%v", consumed)
		}

		// Fifth Backspace after 60ms (< 120ms cooldown guard): JVM GC stutter during repeat storm.
		// Must continue to be swallowed by cooldown guard!
		e.uinputLastIdleBsAt_ = time.Now().Add(-60 * time.Millisecond)
		consumed, err = e.ProcessKeyEvent(IBusBackSpace, IBusBackSpace, 0)
		if err != nil || !consumed {
			t.Fatalf("5th BS during cooldown window (60ms) must still be swallowed, got consumed=%v", consumed)
		}

		// Normal Backspace after delay (> 120ms cooldown) resets burst count and behaves normally
		e.uinputLastIdleBsAt_ = time.Now().Add(-200 * time.Millisecond)
		consumed, err = e.ProcessKeyEvent(IBusBackSpace, IBusBackSpace, 0)
		if err != nil || consumed {
			t.Fatalf("delayed BS after burst (>120ms) should be forwarded to app, got consumed=%v", consumed)
		}

		// Sixth: Rapid Backspace storm while buffer is COMPLETELY EMPTY (rawKeyLen == 0)
		// e.preeditor is now empty (rawKeyLen == 0). A repeat storm here must STILL be swallowed!
		e.resetBuffer()
		if e.getRawKeyLen() != 0 {
			t.Fatalf("expected empty buffer, got %d", e.getRawKeyLen())
		}
		// 1st BS at idle: forwarded
		e.uinputLastIdleBsAt_ = time.Now().Add(-200 * time.Millisecond)
		consumed, err = e.ProcessKeyEvent(IBusBackSpace, IBusBackSpace, 0)
		if err != nil || consumed {
			t.Fatalf("1st idle BS on empty buffer should be forwarded, got %v", consumed)
		}
		// 2nd BS rapid (30ms): forwarded
		e.uinputLastIdleBsAt_ = time.Now().Add(-30 * time.Millisecond)
		consumed, err = e.ProcessKeyEvent(IBusBackSpace, IBusBackSpace, 0)
		if err != nil || consumed {
			t.Fatalf("2nd rapid BS on empty buffer should be forwarded, got %v", consumed)
		}
		// 3rd BS rapid (30ms): RUNAWAY TRIGGERED even on rawKeyLen == 0!
		e.uinputLastIdleBsAt_ = time.Now().Add(-30 * time.Millisecond)
		consumed, err = e.ProcessKeyEvent(IBusBackSpace, IBusBackSpace, 0)
		if err != nil || !consumed {
			t.Fatalf("3rd rapid BS on empty buffer MUST be swallowed by watchdog, got %v", consumed)
		}
	})

	t.Run("jetbrains_ide_auto_mapping_detection", func(t *testing.T) {
		fe := NewFakeEngine()
		engineName := "test-jetbrains-auto"
		cfg := config.DefaultCfg(engineName)
		cfg.DefaultInputMode = config.PreeditIM // default is PreeditIM
		cfg.InputModeMapping = map[string]int{} // empty custom mapping
		inputMethod := bamboo.ParseInputMethod(cfg.InputMethodDefinitions, cfg.InputMethod)
		e := NewIbusBambooEngine(engineName, &cfg, fe, bamboo.NewEngine(inputMethod, cfg.Flags))

		testCases := []struct {
			wmClass  string
			expected int
		}{
			{"jetbrains-idea", config.UinputIM},
			{"jetbrains-webstorm", config.UinputIM},
			{"jetbrains-clion", config.UinputIM},
			{"jetbrains-pycharm-ce", config.UinputIM},
			{"jetbrains-goland", config.UinputIM},
			{"jetbrains-fleet", config.UinputIM},
			{"android-studio", config.UinputIM},
			{"org.gnome.Terminal", config.PreeditIM}, // non-JetBrains falls back to default
		}

		for _, tc := range testCases {
			e.wmClasses = tc.wmClass
			mode := e.getInputMode()
			if mode != tc.expected {
				t.Errorf("getInputMode(%q) = %d, want %d", tc.wmClass, mode, tc.expected)
			}
		}
	})
}
