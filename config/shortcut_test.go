package config

import (
	"encoding/json"
	"testing"
)

func TestShortcutRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		s        string
		mask, kv uint32
		out      string
	}{
		{"Alt+Z", ModAlt, 'z', "Alt+Z"},
		{"Ctrl+Shift+Space", ModCtrl | ModShift, 0x20, "Ctrl+Shift+Space"},
		{"Super+F5", ModSuper, 0xffc2, "Super+F5"},
		{"Ctrl+;", ModCtrl, ';', "Ctrl+;"},
		{"Ctrl+Plus", ModCtrl, '+', "Ctrl+Plus"},
		{"Ctrl++", ModCtrl, '+', "Ctrl+Plus"},
		{"Ctrl+0x00E9", ModCtrl, 0xe9, "Ctrl+0x00E9"},
		{"Alt+~", ModAlt, '~', "Alt+~"},
		{"Ctrl+1", ModCtrl, '1', "Ctrl+1"},
	} {
		var m, k uint32
		parseShortcut(tc.s, &m, &k)
		if m != tc.mask || k != tc.kv {
			t.Errorf("parse %q = %#x/%#x, want %#x/%#x", tc.s, m, k, tc.mask, tc.kv)
		}
		if got := shortcutString(m, k); got != tc.out {
			t.Errorf("string(%q) = %q, want %q", tc.s, got, tc.out)
		}
	}
}

// The GUI records GDK's virtual Super bit (1<<26); it is the same shortcut.
func TestNormalizeMods(t *testing.T) {
	if got := NormalizeMods(1 << 26); got != ModSuper {
		t.Fatalf("Super virtual bit -> %#x", got)
	}
	// Caps Lock, Num Lock, the second layout group and a mouse button are
	// not modifiers of a shortcut.
	if got := NormalizeMods(ModAlt | ModLock | 1<<4 | 1<<13 | 1<<8); got != ModAlt {
		t.Fatalf("lock/group bits kept: %#x", got)
	}
	if shortcutString(1<<26, 'x') != "Super+X" {
		t.Fatalf("virtual Super not printed")
	}
}

func TestShortcutUsable(t *testing.T) {
	for _, tc := range []struct {
		mask, kv uint32
		ok       bool
	}{
		{ModAlt, 'z', true},
		{0, 'z', false},                    // plain letter eats typing
		{ModShift, 'z', false},             // capital letter
		{ModSuper, 0x20, false},            // GNOME input source switch
		{ModSuper | ModShift, 0x20, false}, // backward switch
		{ModCtrl, 0x20, true},
		{0, 0xffe5, false},      // Caps Lock
		{ModAlt, 0xfe08, false}, // ISO_Next_Group (grp:caps_toggle)
		{0, 0xffc2, true},       // F5
		{0, 0, false},
	} {
		if got := ShortcutUsable(tc.mask, tc.kv); got != tc.ok {
			t.Errorf("ShortcutUsable(%#x, %#x) = %v, want %v", tc.mask, tc.kv, got, tc.ok)
		}
	}
}

// Keys missing from an older file keep the defaults instead of turning
// options off.
func TestMissingKeysKeepDefaults(t *testing.T) {
	c := DefaultCfg("ktelex")
	if err := json.Unmarshal([]byte(`{"inputMethod":"Telex 2","spellCheck":true}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.InputMethod != "Telex 2" {
		t.Fatalf("inputMethod %q", c.InputMethod)
	}
	if c.IBflags&IBdisableOnGame == 0 || c.IBflags&IBddFreeStyle == 0 || c.IBflags&IBautoNonVnRestore == 0 {
		t.Fatalf("defaults lost: %#x", c.IBflags)
	}
	if c.OutputCharset != "Unicode" || c.DefaultInputMode != UinputIM {
		t.Fatalf("charset/mode lost: %q %d", c.OutputCharset, c.DefaultInputMode)
	}
	if c.Shortcuts[0] != ModAlt || c.Shortcuts[1] != 'z' {
		t.Fatalf("default shortcut lost: %v", c.Shortcuts)
	}
	// A cleared shortcut stays cleared after a save/load.
	c.Shortcuts[0], c.Shortcuts[1] = 0, 0
	data, _ := json.Marshal(&c)
	d := DefaultCfg("ktelex")
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	if d.Shortcuts[1] != 0 {
		t.Fatalf("cleared shortcut came back: %v", d.Shortcuts)
	}
	// Old files: autoNonVnRestore follows spellCheck when missing.
	e := DefaultCfg("ktelex")
	json.Unmarshal([]byte(`{"spellCheck":false}`), &e)
	if e.IBflags&IBautoNonVnRestore != 0 {
		t.Fatalf("autoNonVnRestore on without spellCheck in an old file")
	}
}
