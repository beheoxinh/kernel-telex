package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMacroParseKeepsColonsInText(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.txt")
	os.WriteFile(p, []byte("# comment\nweb:https://example.com\nhop:10:30 sáng\nVN:Việt Nam\nbad line\n:x\n"), 0o644)
	m := NewMacroTable(true)
	if err := m.LoadFromFile(p); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"web": "https://example.com", "hop": "10:30 sáng", "vn": "Việt Nam", "Vn": "Việt Nam"} {
		if got := m.GetText(k); got != want {
			t.Errorf("%q -> %q, want %q", k, got, want)
		}
	}
	if !m.HasPrefix("V") {
		t.Errorf("HasPrefix ignores auto-capitalize")
	}
}

// Reloading the file while keys are being looked up must not race (it used
// to write the map the key path was reading: fatal error in Go).
func TestMacroReloadWhileTyping(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.txt")
	os.WriteFile(p, []byte("vn:Việt Nam\nko:không\n"), 0o644)
	m := NewMacroTable(true)
	m.LoadFromFile(p)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			m.LoadFromFile(p)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 3000; i++ {
			m.HasPrefix("v")
			m.HasKey("ko")
			m.GetText("vn")
		}
	}()
	wg.Wait()
}

func TestMacroEnableStartsOnePoller(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := NewMacroTable(false)
	m.Enable("ktelex-test")
	first := m.stop
	m.Enable("ktelex-test")
	m.Reload("ktelex-test", true)
	if m.stop != first {
		t.Fatalf("a second poller was started")
	}
	m.Disable()
	if m.stop != nil {
		t.Fatalf("poller not stopped")
	}
}
