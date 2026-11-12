package ui

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>

extern int openGUI(guint flags, int mode, guint32 *s, int size, char *mtext, char *cfgtext, char *appMappings, char *extraConfig);
*/
import "C"

import (
	"fmt"
	"ktelex/config"
	"log"
	"os"
	"unsafe"

	"github.com/BambooEngine/bamboo-core"
)

var engineName string

func update(fn func(c *config.Config)) {
	if err := config.UpdateConfig(engineName, fn); err != nil {
		log.Printf("settings: save failed: %v", err)
	}
}

func writeText(path, text string) {
	if err := config.WriteFileAtomic(path, []byte(text), 0644); err != nil {
		log.Printf("settings: cannot write %s: %v", path, err)
	}
}

//export normalizeShortcutMods
func normalizeShortcutMods(state C.guint) C.guint {
	return C.guint(config.NormalizeMods(uint32(state)))
}

//export shortcutUsable
func shortcutUsable(mask, keyval C.guint) C.int {
	if config.ShortcutUsable(uint32(mask), uint32(keyval)) {
		return 1
	}
	return 0
}

//export saveMacroText
func saveMacroText(text *C.char) {
	writeText(config.GetMacroPath(engineName), C.GoString(text))
}

//export saveInputMode
func saveInputMode(mode int) {
	update(func(c *config.Config) { c.DefaultInputMode = mode })
}

//export saveShortcuts
func saveShortcuts(ptr *C.guint32, length int) {
	codes := makeSliceFromPtr(ptr, length)
	update(func(c *config.Config) { c.Shortcuts = codes })
}

//export saveAppMode
func saveAppMode(appName *C.char, mode int) {
	app := C.GoString(appName)
	update(func(c *config.Config) { c.InputModeMapping[app] = mode })
}

//export removeAppMode
func removeAppMode(appName *C.char) {
	app := C.GoString(appName)
	update(func(c *config.Config) { delete(c.InputModeMapping, app) })
}

//export saveInputMethodName
func saveInputMethodName(name *C.char) {
	im := C.GoString(name)
	update(func(c *config.Config) { c.InputMethod = im })
}

//export saveOutputCharset
func saveOutputCharset(cs *C.char) {
	charset := C.GoString(cs)
	update(func(c *config.Config) { c.OutputCharset = charset })
}

//export saveIBFlag
func saveIBFlag(flag C.guint, enabled C.int) {
	update(func(c *config.Config) {
		if enabled != 0 {
			c.IBflags |= uint(flag)
		} else {
			c.IBflags &^= uint(flag)
		}
	})
}

//export saveCoreFlag
func saveCoreFlag(flag C.guint, enabled C.int) {
	update(func(c *config.Config) {
		if enabled != 0 {
			c.Flags |= uint(flag)
		} else {
			c.Flags &^= uint(flag)
		}
	})
}

func makeSliceFromPtr(ptr *C.guint32, size int) [10]uint32 {
	var out [10]uint32
	slice := (*[1 << 28]C.guint32)(unsafe.Pointer(ptr))[:size:size]
	for i, elem := range slice[:size] {
		out[i] = uint32(elem)
	}
	return out
}

func OpenGUI(engName string, version string, buildId string) {
	// Always use the master (VI) config — EN engine never persists its runtime
	// DefaultInputMode override.  All save callbacks write to the shared file.
	engineName = config.MasterName(engName)
	cfg := config.LoadConfig(engineName)

	// Build version display string
	verDisplay := version
	if buildId != "" {
		verDisplay = "v" + version + " - Build " + buildId
	}

	shortcuts := cfg.Shortcuts[:]
	s := (*C.guint32)(&shortcuts[0])

	macroFP := config.GetMacroPath(engineName)
	mText, err := os.ReadFile(macroFP)
	if err != nil && !os.IsNotExist(err) {
		log.Printf("settings: cannot read %s: %v", macroFP, err)
	}

	var appMappings string
	for app, mode := range cfg.InputModeMapping {
		if appMappings != "" {
			appMappings += ";"
		}
		appMappings += fmt.Sprintf("%s|%d", app, mode)
	}

	// Build extra config for General tab
	// Format: im|cs|flags|macro|cap|spell|rules|dicts|preedit|game|imList|csList
	ib := cfg.IBflags
	extra := fmt.Sprintf("%s|%s|%d|%d|%d|%d|%d|%d|%d|%d|",
		cfg.InputMethod, cfg.OutputCharset, cfg.Flags,
		(ib>>1)&1,  // macro_enabled (bit 1)
		(ib>>15)&1, // auto_capitalize (IBautoCapitalizeMacro, bit 15)
		(ib>>4)&1,  // spell_check (bit 4)
		(ib>>8)&1,  // spell_rules (bit 8)
		(ib>>9)&1,  // spell_dicts (bit 9)
		(ib>>10)&1, // preedit_elimination (bit 10)
		(ib>>21)&1, // disable_on_game (bit 21)
	)
	var imList, csList string
	for name := range cfg.InputMethodDefinitions {
		if imList != "" {
			imList += ";"
		}
		imList += name
	}
	for _, name := range bamboo.GetCharsetNames() {
		if csList != "" {
			csList += ";"
		}
		csList += name
	}
	extra += imList + "|" + csList

	C.openGUI(
		C.guint(cfg.IBflags),
		C.int(cfg.DefaultInputMode),
		s,
		C.int(len(shortcuts)),
		C.CString(string(mText)),
		C.CString(verDisplay),
		C.CString(appMappings),
		C.CString(extra),
	)
}
