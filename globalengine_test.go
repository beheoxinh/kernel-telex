package main

import (
	"sync"
	"testing"
	"time"
)

type healHarness struct {
	mu    sync.Mutex
	calls []string
}

func setupHeal(t *testing.T, gnomeSrc string, gnomeOK bool) *healHarness {
	t.Helper()
	h := &healHarness{}
	healMu.Lock()
	activeEngineName, healPending, healAttempts = "", false, 0
	processStartAt = time.Now()
	gnomeActiveSourceHook = func() (string, bool) { return gnomeSrc, gnomeOK }
	setGlobalEngineHook = func(id string) error {
		h.mu.Lock()
		h.calls = append(h.calls, id)
		h.mu.Unlock()
		return nil
	}
	healMu.Unlock()
	t.Cleanup(func() {
		time.Sleep(kHealDelay + 50*time.Millisecond) // let pending checks drain
		healMu.Lock()
		gnomeActiveSourceHook = nil
		setGlobalEngineHook = nil
		healMu.Unlock()
	})
	return h
}

func (h *healHarness) got() []string {
	time.Sleep(kHealDelay + 150*time.Millisecond)
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

// Login desync: GNOME selected KTelex but ibus activated KtelexUs.
func TestHealSwitchesToVietnamese(t *testing.T) {
	h := setupHeal(t, "KTelex", true)
	noteEngineActive("ktelexus")
	if c := h.got(); len(c) != 1 || c[0] != viEngineID {
		t.Fatalf("SetGlobalEngine calls=%v, want [%s]", c, viEngineID)
	}
}

// The user really chose English: never revert it.
func TestHealRespectsEnglishChoice(t *testing.T) {
	h := setupHeal(t, "KtelexUs", true)
	noteEngineActive("ktelexus")
	if c := h.got(); len(c) != 0 {
		t.Fatalf("reverted a deliberate English choice: %v", c)
	}
}

// The Vietnamese engine took over before the check ran.
func TestHealCancelledWhenVietnameseActive(t *testing.T) {
	h := setupHeal(t, "KTelex", true)
	noteEngineActive("ktelexus")
	noteEngineActive("ktelex")
	if c := h.got(); len(c) != 0 {
		t.Fatalf("healed although VI engine was active: %v", c)
	}
}

// Only during the start-up window.
func TestHealOnlyAfterStartup(t *testing.T) {
	h := setupHeal(t, "KTelex", true)
	healMu.Lock()
	processStartAt = time.Now().Add(-2 * kHealWindow)
	healMu.Unlock()
	noteEngineActive("ktelexus")
	if c := h.got(); len(c) != 0 {
		t.Fatalf("healed outside the start-up window: %v", c)
	}
}

// Bounded attempts (no fight with the desktop).
func TestHealBoundedAttempts(t *testing.T) {
	h := setupHeal(t, "KTelex", true)
	for i := 0; i < kHealMaxAttempts+2; i++ {
		noteEngineActive("ktelexus")
		h.got()
	}
	if c := h.got(); len(c) != kHealMaxAttempts {
		t.Fatalf("calls=%d, want %d", len(c), kHealMaxAttempts)
	}
}

func TestParseFirstIbusSource(t *testing.T) {
	for in, want := range map[string]string{
		"[('ibus', 'KTelex'), ('ibus', 'KtelexUs')]\n": "KTelex",
		"[('ibus','KtelexUs')]":                        "KtelexUs",
		"[('xkb', 'us'), ('ibus', 'KTelex')]":          "",
		"@a(ss) []":                                    "",
	} {
		got, ok := parseFirstIbusSource(in)
		if got != want || ok != (want != "") {
			t.Errorf("parse(%q)=%q,%v want %q", in, got, ok, want)
		}
	}
}
