package main

import (
	"os"
	"testing"

	"ktelex/config"

	ibus "github.com/BambooEngine/goibus"
)

// Kernel mode: English words come back at the word break; Vietnamese words
// and the Telex "double key = literal" idiom are kept.
func TestUinputRestoreEnglishAtWordBreak(t *testing.T) {
	for word, want := range map[string]string{
		"coffee ":  "coffee ",
		"text ":    "text ",
		"loop ":    "loop ",
		"keep,":    "keep,",
		"law ":     "law ",
		"window ":  "window ",
		"user ":    "user ",
		"tieengs ": "tiếng ",
		"vieejt ":  "việt ",
		"dduowcj ": "được ",
		"hoaf ":    "hòa ",
		"ass ":     "as ",
		"off ":     "of ",
		"tooi ":    "tôi ",
		"ddi ":     "đi ",
		"cos ":     "có ",
		"anw ":     "ăn ",
		"hocj ":    "học ",
		"bachs ":   "bách ",
	} {
		a := newAppModel(t)
		a.drivePerfectEcho(word)
		a.awaitIdle(2e9)
		if got := a.text(); got != want {
			t.Errorf("%q: got %q, want %q", word, got, want)
		}
	}
}

// Restore can be switched off.
func TestUinputRestoreEnglishOff(t *testing.T) {
	a := newAppModel(t)
	a.e.config.IBflags &^= config.IBautoNonVnRestore
	a.drivePerfectEcho("text ")
	a.awaitIdle(2e9)
	if got := a.text(); got != "tẽt " {
		t.Fatalf("got %q, want %q", got, "tẽt ")
	}
}

func TestVnSyllableComplete(t *testing.T) {
	for w, ok := range map[string]bool{
		"lôp": false, "kêp": false, "tẽt": false, "lă": false, "lâ": false, "tấ": false,
		"lốp": true, "kệp": true, "ách": true, "ạch": true, "ăn": true, "ấy": true, "câu": true,
		"việt": true, "học": true, "a": true, "ba": true, "mẹ": true, "tôi": true, "xin chào": true,
	} {
		if got := vnSyllableComplete(w); got != ok {
			t.Errorf("vnSyllableComplete(%q) = %v, want %v", w, got, ok)
		}
	}
}

// Preedit mode commits the English word at the break too.
func TestPreeditRestoreEnglishAtWordBreak(t *testing.T) {
	for word, want := range map[string]string{
		"loop ": "loop ", "coffee ": "coffee ", "text ": "text ", "tieengs ": "tiếng ", "ass ": "as ",
	} {
		e, fe := newUinputEchoEngine(t)
		e.wmClasses = "preedit-app:Preedit-app"
		e.config.InputModeMapping = map[string]int{"preedit-app:Preedit-app": config.PreeditIM}
		e.bumpConfigGen()
		for _, k := range word {
			e.ProcessKeyEvent(uint32(k), 0, 0)
		}
		fe.mu.Lock()
		got := fe.commitText
		fe.mu.Unlock()
		if got != want {
			t.Errorf("%q: committed %q, want %q", word, got, want)
		}
	}
}

// Telex 2: w, [ and ] type ư/ơ on their own in Kernel mode.
func TestUinputTelex2(t *testing.T) {
	for word, want := range map[string]string{
		"w ": "ư ", "tw ": "tư ", "[ ": "ơ ", "]": "ư", "[[": "[", "mwa ": "mưa ",
	} {
		a := newAppModel(t)
		a.e.PropertyActivate("Telex 2", ibus.PROP_STATE_CHECKED)
		a.drivePerfectEcho(word)
		a.awaitIdle(2e9)
		if got := a.text(); got != want {
			t.Errorf("%q: got %q, want %q", word, got, want)
		}
	}
}

func TestGameByWmClass(t *testing.T) {
	for wm, want := range map[string]bool{
		"steam_app_570:steam_app_570": true, "dota2:dota2": true, "cs2:cs2": true,
		"retroarch:RetroArch": true, "dolphin-emu:dolphin-emu": true, "gamescope:gamescope": true,
		"protonmail:Proton Mail": false, "proton-pass:Proton Pass": false, "docs2:docs2": false,
		"winecfg.exe:Wine": false, "firefox:firefox": false, "steamwebhelper:steam": false,
		"org.gnome.TextEditor:Org.gnome.TextEditor": false, "": false,
	} {
		if got := isGameByWmClass(wm); got != want {
			t.Errorf("isGameByWmClass(%q) = %v, want %v", wm, got, want)
		}
	}
}

func TestGameProcessSelf(t *testing.T) {
	// The test process is no game and descends from no launcher.
	if isGameProcess(os.Getpid()) {
		t.Fatalf("test process classified as game")
	}
	if isGameProcess(0) || isGameProcess(1) {
		t.Fatalf("pid 0/1 classified as game")
	}
}
