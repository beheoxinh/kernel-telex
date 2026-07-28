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
	"io/ioutil"
	"unsafe"

	"github.com/BambooEngine/bamboo-core"
)

var engineName string

//export saveFlags
func saveFlags(flags C.guint) {
	if flags == 0 {
		return
	}
	cfg := config.LoadConfig(engineName)
	if cfg.IBflags&uint(flags) != 0 {
		cfg.IBflags &^= uint(flags)
	} else {
		cfg.IBflags |= uint(flags)
	}
	config.SaveConfig(cfg, engineName)
}

//export saveConfigText
func saveConfigText(text *C.char) {
	cfgText := C.GoString(text)
	cfgFn := config.GetConfigPath(engineName)
	err := ioutil.WriteFile(cfgFn, []byte(cfgText), 0644)
	if err != nil {
		panic(err)
	}
}

//export saveMacroText
func saveMacroText(text *C.char) {
	macroText := C.GoString(text)
	macroFP := config.GetMacroPath(engineName)
	err := ioutil.WriteFile(macroFP, []byte(macroText), 0644)
	if err != nil {
		panic(err)
	}
}

//export saveInputMode
func saveInputMode(mode int) {
	cfg := config.LoadConfig(engineName)
	cfg.DefaultInputMode = mode
	config.SaveConfig(cfg, engineName)
}

//export saveShortcuts
func saveShortcuts(ptr *C.guint32, length int) {
	cfg := config.LoadConfig(engineName)
	codes := makeSliceFromPtr(ptr, length)
	cfg.Shortcuts = codes
	config.SaveConfig(cfg, engineName)
}

//export saveAppMode
func saveAppMode(appName *C.char, mode int) {
	cfg := config.LoadConfig(engineName)
	cfg.InputModeMapping[C.GoString(appName)] = mode
	config.SaveConfig(cfg, engineName)
}

//export removeAppMode
func removeAppMode(appName *C.char) {
	cfg := config.LoadConfig(engineName)
	delete(cfg.InputModeMapping, C.GoString(appName))
	config.SaveConfig(cfg, engineName)
}

//export saveInputMethodName
func saveInputMethodName(name *C.char) {
	cfg := config.LoadConfig(engineName)
	cfg.InputMethod = C.GoString(name)
	config.SaveConfig(cfg, engineName)
}

//export saveOutputCharset
func saveOutputCharset(cs *C.char) {
	cfg := config.LoadConfig(engineName)
	cfg.OutputCharset = C.GoString(cs)
	config.SaveConfig(cfg, engineName)
}

//export saveIBFlag
func saveIBFlag(flag C.guint, enabled C.int) {
	cfg := config.LoadConfig(engineName)
	if enabled != 0 {
		cfg.IBflags |= uint(flag)
	} else {
		cfg.IBflags &= ^uint(flag)
	}
	config.SaveConfig(cfg, engineName)
}

//export saveCoreFlag
func saveCoreFlag(flag C.guint, enabled C.int) {
	cfg := config.LoadConfig(engineName)
	if enabled != 0 {
		cfg.Flags |= uint(flag)
	} else {
		cfg.Flags &= ^uint(flag)
	}
	config.SaveConfig(cfg, engineName)
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
	mText, err := ioutil.ReadFile(macroFP)
	if err != nil {
		panic(err)
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
		(ib>>1)&1,   // macro_enabled (bit 1)
		(ib>>18)&1,  // auto_capitalize (existing convention: bit 18)
		(ib>>4)&1,   // spell_check (bit 4)
		(ib>>8)&1,   // spell_rules (bit 8)
		(ib>>9)&1,   // spell_dicts (bit 9)
		(ib>>10)&1,  // preedit_elimination (bit 10)
		(ib>>21)&1,  // disable_on_game (bit 21)
	)
	var imList, csList string
	for name := range cfg.InputMethodDefinitions {
		if imList != "" { imList += ";" }
		imList += name
	}
	for _, name := range bamboo.GetCharsetNames() {
		if csList != "" { csList += ";" }
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
