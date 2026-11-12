package main

import (
	"testing"

	"ktelex/config"
)

// Caps Lock (grp:caps_toggle -> ISO_Next_Group) is the user's VI/EN switch:
// the engine never consumes it, in any mode, and it does not touch the
// syllable being typed.
func TestCapsLockAndGroupKeysPassThrough(t *testing.T) {
	a := newAppModel(t)
	a.drivePerfectEcho("tieen")
	for _, k := range []uint32{IBusCapsLock, 0xfe08, 0xfe0a, 0xfe0c} {
		if ret, _ := a.e.ProcessKeyEvent(k, 58, 0); ret {
			t.Fatalf("key %#x consumed", k)
		}
	}
	a.e.Lock()
	got := a.e.uinputPreeditString()
	a.e.Unlock()
	if got != "tiên" {
		t.Fatalf("syllable changed by a lock key: %q", got)
	}
	// Emoji picker open: lock keys keep the query.
	a.e.Lock()
	a.e.isEmojiLTOpened = true
	a.e.Unlock()
	if ret, _ := a.e.ProcessKeyEvent(0xfe08, 58, 0); ret {
		t.Fatalf("group key consumed by the emoji picker")
	}
}

// Shortcuts match whatever the Caps Lock / Num Lock / layout group state.
func TestShortcutIgnoresLockAndGroup(t *testing.T) {
	e, _ := newUinputEchoEngine(t)
	e.wmClasses = "gedit:Gedit"
	for _, st := range []uint32{IBusMod1Mask, IBusMod1Mask | IBusLockMask, IBusMod1Mask | 1<<4, IBusMod1Mask | 1<<13} {
		e.Lock()
		ok := e.isShortcutKeyPressed('z', st, KSInputModeSwitch)
		e.Unlock()
		if !ok {
			t.Errorf("Alt+Z with state %#x not recognised", st)
		}
	}
	// Super stored as GDK's virtual bit (old GUI) matches IBus Mod4 and the
	// virtual bit alike.
	e.config.Shortcuts[KSEmojiDialog], e.config.Shortcuts[KSEmojiDialog+1] = 1<<26, 'e'
	for _, st := range []uint32{IBusMod4Mask, IBusSuperMask, IBusMod4Mask | IBusSuperMask} {
		if !e.isShortcutKeyPressed('e', st, KSEmojiDialog) {
			t.Errorf("Super+E with state %#x not recognised", st)
		}
	}
	// A plain letter can never be a shortcut (it would eat typing).
	e.config.Shortcuts[KSViEnSwitch], e.config.Shortcuts[KSViEnSwitch+1] = 0, 'a'
	if e.isShortcutKeyEnable(KSViEnSwitch) {
		t.Fatalf("plain letter accepted as a shortcut")
	}
	// Super+Space belongs to GNOME.
	e.config.Shortcuts[KSViEnSwitch], e.config.Shortcuts[KSViEnSwitch+1] = config.ModSuper, ' '
	if e.isShortcutKeyEnable(KSViEnSwitch) {
		t.Fatalf("Super+Space accepted as a shortcut")
	}
}

// Emoji picker: digits without a candidate stay in the query; modifiers do
// not end it; cursor index bounds are exact.
func TestEmojiPickerKeys(t *testing.T) {
	ensureEmojiTrie()
	e, fe := newUinputEchoEngine(t)
	e.Lock()
	defer e.Unlock()
	e.isEmojiLTOpened = true
	e.emoji = NewEmojiEngine()
	e.openEmojiList()
	for _, k := range "smile" {
		e.emojiProcessKeyEvent(uint32(k), 0, 0)
	}
	if e.emojiProcessKeyEvent(0xffe1, 0, 0) { // Shift
		t.Fatalf("Shift consumed")
	}
	if !e.isEmojiLTOpened || e.emoji.GetRawString() == "" {
		t.Fatalf("Shift closed the picker")
	}
	n := len(e.emojiLookupTable.Candidates)
	if n == 0 {
		t.Skip("no emoji data")
	}
	if e.updateCursorPosInEmojiTable(e.emojiLookupTable.PageSize) {
		t.Fatalf("index == page size accepted")
	}
	if n < 9 && e.updateCursorPosInEmojiTable(uint32(n)) {
		t.Fatalf("index == candidate count accepted")
	}
	if !e.emojiProcessKeyEvent('1', 0, 0) {
		t.Fatalf("digit 1 did not pick")
	}
	fe.mu.Lock()
	got := fe.commitText
	fe.mu.Unlock()
	if got == "" {
		t.Fatalf("nothing committed")
	}
}

// Focus flaps inside one context (ONLYOFFICE, browsers) do not resend the
// property list; another engine taking focus or a new list does.
func TestFocusInSkipsUnchangedProperties(t *testing.T) {
	focusPropsOwner.Store(nil)
	e, fe := newUinputEchoEngine(t)
	count := func() int { fe.mu.Lock(); defer fe.mu.Unlock(); return fe.registerProps }
	for i := 0; i < 5; i++ {
		e.FocusOut()
		e.FocusIn()
	}
	if n := count(); n != 1 {
		t.Fatalf("5 focus flaps sent %d property lists, want 1", n)
	}
	other, _ := newUinputEchoEngine(t)
	other.FocusIn()
	e.FocusIn()
	if n := count(); n != 2 {
		t.Fatalf("refocus after another engine: %d lists, want 2", n)
	}
	e.Lock()
	e.propList = GetPropListByConfig(e.config)
	e.Unlock()
	e.FocusIn()
	if n := count(); n != 3 {
		t.Fatalf("changed list not resent: %d", n)
	}
}
