package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/BambooEngine/bamboo-core"
)

const (
	configDir        = "%s/.config/ibus-%s"
	configFile       = "%s/ibus-%s.config.json"
	mactabFile       = "%s/ibus-%s.macro.text"
	sampleMactabFile = "data/macro.tpl.txt"
)

type Config struct {
	InputMethod            string
	InputMethodDefinitions map[string]bamboo.InputMethodDefinition
	OutputCharset          string
	Flags                  uint
	IBflags                uint
	Shortcuts              [10]uint32
	DefaultInputMode       int
	InputModeMapping       map[string]int
	// UinputSelectApps: apps (normalized WM_CLASS, trailing '*' = prefix)
	// where Kernel mode replaces a whole syllable by selecting it with
	// Shift+Left instead of Backspace, so the field never becomes empty.
	// nil = DefaultUinputSelectApps, empty list = disabled. The entry
	// "@default" expands to DefaultUinputSelectApps, so a saved config keeps
	// following the built-in list as it grows.
	UinputSelectApps []string
}

// DefaultUinputSelectApps is empty: the Shift+Left strategy is opt-in.
// Arrow keys mean something to the app itself (Excel/Sheets extend the cell
// selection with Shift+Left; a terminal turns it into an escape sequence),
// and rich editors that drop a commit after an emptied field are served by
// the surrounding-text replace (see uinputTryDstLocked) instead.
var DefaultUinputSelectApps = []string{}

// DefaultUinputCompletionApps: browsers, whose address and search bars
// select an inline completion after the caret. Kernel mode clears it before
// deleting (see uinputBeginTxLocked). Not configurable on purpose: the
// workaround is harmless where no completion is shown.
var DefaultUinputCompletionApps = []string{
	"google-chrome", "google-chrome-beta", "google-chrome-unstable",
	"chromium", "chromium-browser", "thorium", "thorium-browser",
	"brave-browser", "microsoft-edge", "microsoft-edge-beta",
	"vivaldi", "vivaldi-stable", "opera", "yandex-browser",
	"firefox*", "org.mozilla.firefox", "navigator", "floorp*", "librewolf*",
	"zen*", "app.zen_browser.zen", "waterfox*", "mullvadbrowser",
}

// UinputSelectDefaultToken in UinputSelectApps stands for the default list.
const UinputSelectDefaultToken = "@default"

// configJSON is the human-readable JSON surface.
type configJSON struct {
	InputMethod   string `json:"inputMethod"`
	OutputCharset string `json:"outputCharset"`

	// ── bamboo-core Flags ──
	FreeToneMarking bool `json:"freeToneMarking"`
	StdToneStyle    bool `json:"stdToneStyle"`
	AutoCorrect     bool `json:"autoCorrect"`

	// ── IBus feature toggles ──
	UseMacro            bool `json:"useMacro"`
	AutoCapitalizeMacro bool `json:"autoCapitalizeMacro"`
	SpellCheck          bool `json:"spellCheck"`
	SpellCheckByRules   bool `json:"spellCheckByRules"`
	SpellCheckByDicts   bool `json:"spellCheckByDicts"`
	AutoNonVnRestore    bool `json:"autoNonVnRestore"`
	DdFreeStyle         bool `json:"ddFreeStyle"`
	DisableOnGame       bool `json:"disableOnGame"`

	// ── Shortcut keys ──
	// "" = no shortcut. Always written, so a cleared shortcut stays cleared
	// instead of falling back to the default.
	InputModeSwitch   string `json:"inputModeSwitch"`
	RestoreKeystrokes string `json:"restoreKeystrokes"`
	ViEnSwitch        string `json:"viEnSwitch"`
	EmojiDialog       string `json:"emojiDialog"`

	DefaultInputMode int            `json:"defaultInputMode"`
	InputModeMapping map[string]int `json:"inputModeMapping,omitempty"`
	UinputSelectApps []string       `json:"uinputSelectApps"`
}

func (c *Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.toJSON())
}

func (c *Config) toJSON() configJSON {
	j := configJSON{
		InputMethod:         c.InputMethod,
		OutputCharset:       c.OutputCharset,
		FreeToneMarking:     c.Flags&bamboo.EfreeToneMarking != 0,
		StdToneStyle:        c.Flags&bamboo.EstdToneStyle != 0,
		AutoCorrect:         c.Flags&bamboo.EautoCorrectEnabled != 0,
		UseMacro:            c.IBflags&IBmacroEnabled != 0,
		AutoCapitalizeMacro: c.IBflags&IBautoCapitalizeMacro != 0,
		SpellCheck:          c.IBflags&IBspellCheckEnabled != 0,
		SpellCheckByRules:   c.IBflags&IBspellCheckWithRules != 0,
		SpellCheckByDicts:   c.IBflags&IBspellCheckWithDicts != 0,
		AutoNonVnRestore:    c.IBflags&IBautoNonVnRestore != 0,
		DdFreeStyle:         c.IBflags&IBddFreeStyle != 0,
		DisableOnGame:       c.IBflags&IBdisableOnGame != 0,
		InputModeSwitch:     shortcutString(c.Shortcuts[0], c.Shortcuts[1]),
		RestoreKeystrokes:   shortcutString(c.Shortcuts[2], c.Shortcuts[3]),
		ViEnSwitch:          shortcutString(c.Shortcuts[4], c.Shortcuts[5]),
		EmojiDialog:         shortcutString(c.Shortcuts[6], c.Shortcuts[7]),
		DefaultInputMode:    c.DefaultInputMode,
		InputModeMapping:    c.InputModeMapping,
		UinputSelectApps:    c.UinputSelectApps,
	}
	if j.UinputSelectApps == nil {
		j.UinputSelectApps = []string{UinputSelectDefaultToken}
	}
	return j
}

func (c *Config) UnmarshalJSON(data []byte) error {
	// Detect old-format config (has "IBflags" or "Shortcuts" as raw numbers)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	_, hasOldFlags := raw["IBflags"]
	_, hasOldShortcuts := raw["Shortcuts"]

	if hasOldFlags || hasOldShortcuts {
		// Old format: decode into the struct directly (fields match old name)
		type oldConfig Config
		return json.Unmarshal(data, (*oldConfig)(c))
	}

	// New human-readable format
	// Detect which explicit keys exist for backward-compat decisions
	_, hasAutoNonVn := raw["autoNonVnRestore"]

	// Keys missing from the file keep the values c already holds (the
	// defaults, when called from LoadConfig): a file written by an older
	// version must not turn new options off.
	j := c.toJSON()
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	if !hasAutoNonVn {
		j.AutoNonVnRestore = j.SpellCheck
	}
	if j.InputMethod != "" {
		c.InputMethod = j.InputMethod
	}
	if j.OutputCharset != "" {
		c.OutputCharset = j.OutputCharset
	}
	c.DefaultInputMode = j.DefaultInputMode
	c.InputModeMapping = j.InputModeMapping
	if _, ok := raw["uinputSelectApps"]; ok {
		c.UinputSelectApps = j.UinputSelectApps
		if c.UinputSelectApps == nil { // JSON null
			c.UinputSelectApps = []string{}
		}
	}

	c.IBflags = 0
	if j.UseMacro {
		c.IBflags |= IBmacroEnabled
	}
	if j.AutoCapitalizeMacro {
		c.IBflags |= IBautoCapitalizeMacro
	}
	if j.SpellCheck {
		c.IBflags |= IBspellCheckEnabled
	}
	if j.AutoNonVnRestore {
		c.IBflags |= IBautoNonVnRestore
	}
	if j.DdFreeStyle {
		c.IBflags |= IBddFreeStyle
	}
	if j.SpellCheckByRules {
		c.IBflags |= IBspellCheckWithRules
	}
	if j.SpellCheckByDicts {
		c.IBflags |= IBspellCheckWithDicts
	}
	// WPS workaround always enabled
	c.IBflags |= IBworkaroundForWPS
	if j.DisableOnGame {
		c.IBflags |= IBdisableOnGame
	}

	c.Flags = 0
	if j.StdToneStyle {
		c.Flags |= bamboo.EstdToneStyle
	}
	if j.FreeToneMarking {
		c.Flags |= bamboo.EfreeToneMarking
	}
	if j.AutoCorrect {
		c.Flags |= bamboo.EautoCorrectEnabled
	}

	c.Shortcuts = [10]uint32{}
	parseShortcut(j.InputModeSwitch, &c.Shortcuts[0], &c.Shortcuts[1])
	parseShortcut(j.RestoreKeystrokes, &c.Shortcuts[2], &c.Shortcuts[3])
	parseShortcut(j.ViEnSwitch, &c.Shortcuts[4], &c.Shortcuts[5])
	parseShortcut(j.EmojiDialog, &c.Shortcuts[6], &c.Shortcuts[7])

	return nil
}

// ── Shortcut helpers ──────────────────────────────────────────────

// Modifier bits of a shortcut (IBus/GDK state bits). Super is stored as
// Mod4; the GUI and IBus may also report the virtual Super/Hyper/Meta bits.
const (
	ModShift uint32 = 1 << 0
	ModLock  uint32 = 1 << 1
	ModCtrl  uint32 = 1 << 2
	ModAlt   uint32 = 1 << 3 // Mod1
	ModSuper uint32 = 1 << 6 // Mod4

	modVirtSuper uint32 = 1 << 26
	modVirtHyper uint32 = 1 << 27
	modVirtMeta  uint32 = 1 << 28
)

// NormalizeMods reduces a key state to the modifiers a shortcut is made of:
// Shift, Ctrl, Alt and Super. Caps Lock, Num Lock, the keyboard group (the
// layout Caps Lock switches to with grp:caps_toggle) and mouse buttons are
// dropped, so they never stop a shortcut from matching.
func NormalizeMods(state uint32) uint32 {
	m := state & (ModShift | ModCtrl | ModAlt)
	if state&(ModSuper|modVirtSuper|modVirtHyper) != 0 {
		m |= ModSuper
	}
	if state&modVirtMeta != 0 {
		m |= ModAlt
	}
	return m
}

// Keysyms that never make a shortcut.
const (
	keyCapsLock  = 0xffe5
	keySpace     = 0x20
	keyEscape    = 0xff1b
	keyBackSpace = 0xff08
)

// IsLockOrGroupKey: Caps/Num/Shift Lock and the ISO group/level keys. With
// grp:caps_toggle Caps Lock sends ISO_Next_Group to switch the input source.
func IsLockOrGroupKey(keyval uint32) bool {
	return keyval == keyCapsLock || keyval == 0xff7f || keyval == 0xffe6 ||
		(keyval >= 0xfe01 && keyval <= 0xfe0f)
}

// ShortcutUsable reports whether mask+keyval can be a shortcut without
// eating ordinary typing or a system key: a printable key needs Ctrl, Alt
// or Super; lock/group keys (the user's Caps Lock layout switch) and
// Super+Space / Shift+Super+Space (GNOME's input source switch) are refused.
func ShortcutUsable(mask, keyval uint32) bool {
	if keyval == 0 || IsLockOrGroupKey(keyval) || keyval == keyEscape || keyval == keyBackSpace {
		return false
	}
	m := NormalizeMods(mask)
	if keyval == keySpace && m&ModSuper != 0 && m&(ModCtrl|ModAlt) == 0 {
		return false
	}
	if (keyval >= 0x20 && keyval <= 0x7e) || (keyval >= 0xa0 && keyval <= 0xff) || keyval >= 0x1000000 {
		return m&(ModCtrl|ModAlt|ModSuper) != 0
	}
	return true
}

// keyNames are the keysym names used in the JSON besides plain characters.
var keyNames = map[string]uint32{
	"Space": 0x20, "Plus": 0x2b, "Tab": 0xff09, "Return": 0xff0d, "Escape": 0xff1b,
	"BackSpace": 0xff08, "Delete": 0xffff, "Insert": 0xff63, "Home": 0xff50, "End": 0xff57,
	"PageUp": 0xff55, "PageDown": 0xff56, "Left": 0xff51, "Up": 0xff52, "Right": 0xff53, "Down": 0xff54,
	"Shift_L": 0xffe1, "Shift_R": 0xffe2, "Control_L": 0xffe3, "Control_R": 0xffe4,
	"Alt_L": 0xffe9, "Alt_R": 0xffea, "Super_L": 0xffeb, "Super_R": 0xffec,
}

var revKeyNames map[uint32]string

func init() {
	for i := uint32(1); i <= 24; i++ {
		keyNames[fmt.Sprintf("F%d", i)] = 0xffbd + i
	}
	revKeyNames = make(map[uint32]string, len(keyNames))
	for k, v := range keyNames {
		revKeyNames[v] = k
	}
}

func parseShortcut(s string, mask, keyval *uint32) {
	*mask = 0
	*keyval = 0
	if s == "" {
		return
	}
	var parts []string
	if strings.HasSuffix(s, "++") { // "Ctrl++" (old spelling of Plus)
		parts = append(strings.Split(strings.TrimSuffix(s, "++"), "+"), "+")
	} else {
		parts = strings.Split(s, "+")
	}
	for i, p := range parts {
		if i < len(parts)-1 {
			switch strings.ToLower(p) {
			case "ctrl", "control":
				*mask |= ModCtrl
			case "alt":
				*mask |= ModAlt
			case "shift":
				*mask |= ModShift
			case "super", "meta", "win":
				*mask |= ModSuper
			}
			continue
		}
		switch {
		case p == "":
		case strings.HasPrefix(p, "0x") || strings.HasPrefix(p, "0X"):
			var kv uint32
			if _, err := fmt.Sscanf(p[2:], "%x", &kv); err == nil {
				*keyval = kv
			}
		case keyNames[p] != 0:
			*keyval = keyNames[p]
		case len([]rune(p)) == 1:
			// The GUI stores lower-case keysyms (gdk_keyval_to_lower).
			r := []rune(strings.ToLower(p))[0]
			if r < 0x100 {
				*keyval = uint32(r)
			} else {
				*keyval = uint32(r) | 0x1000000
			}
		default:
			// "Shift" alone etc.: a modifier used as the key.
			switch strings.ToLower(p) {
			case "shift":
				*keyval = 0xffe1
			case "ctrl", "control":
				*keyval = 0xffe3
			case "alt":
				*keyval = 0xffe9
			case "super":
				*keyval = 0xffeb
			}
		}
	}
	*mask = NormalizeMods(*mask)
}

func shortcutString(mask, keyval uint32) string {
	if mask == 0 && keyval == 0 {
		return ""
	}
	mask = NormalizeMods(mask)
	var parts []string
	if mask&ModCtrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if mask&ModAlt != 0 {
		parts = append(parts, "Alt")
	}
	if mask&ModShift != 0 {
		parts = append(parts, "Shift")
	}
	if mask&ModSuper != 0 {
		parts = append(parts, "Super")
	}
	if keyval > 0 {
		if name, ok := revKeyNames[keyval]; ok {
			parts = append(parts, name)
		} else if keyval > 0x20 && keyval <= 0x7e {
			parts = append(parts, strings.ToUpper(string(rune(keyval))))
		} else if keyval >= 0x1000100 && keyval <= 0x110ffff {
			parts = append(parts, strings.ToUpper(string(rune(keyval&0xffffff))))
		} else {
			parts = append(parts, fmt.Sprintf("0x%04X", keyval))
		}
	}
	return strings.Join(parts, "+")
}

// ── Path helpers ──────────────────────────────────────────────────

func GetConfigDir(ngName string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return fmt.Sprintf(configDir, home, "ktelex")
	}
	u, err := user.Current()
	if err == nil {
		return fmt.Sprintf(configDir, u.HomeDir, "ktelex")
	}
	return fmt.Sprintf(configDir, "~", "ktelex")
}

func GetMacroPath(engineName string) string {
	// Macro file is shared between VI and EN engines
	return fmt.Sprintf(mactabFile, GetConfigDir("ktelex"), MasterName(engineName))
}

func GetConfigPath(engineName string) string {
	return fmt.Sprintf(configFile, GetConfigDir(engineName), engineName)
}

func DefaultCfg(engineName string) Config {
	return Config{
		InputMethod:            "Telex",
		OutputCharset:          "Unicode",
		InputMethodDefinitions: bamboo.GetInputMethodDefinitions(),
		Flags:                  bamboo.EstdFlags,
		IBflags:                IBstdFlags,
		Shortcuts:              [10]uint32{8, 122, 0, 0, 0, 0, 0, 0, 0, 0},
		DefaultInputMode:       UinputIM,
		InputModeMapping: map[string]int{
			"jetbrains-idea":     UinputIM,
			"jetbrains-webstorm": UinputIM,
			"jetbrains-pycharm":  UinputIM,
			"jetbrains-goland":   UinputIM,
			"jetbrains-clion":    UinputIM,
			"jetbrains-phpstorm": UinputIM,
			"jetbrains-rider":    UinputIM,
			"jetbrains-datagrip": UinputIM,
			"jetbrains-fleet":    UinputIM,
			"android-studio":     UinputIM,
		},
	}
}

func MasterName(ngName string) string {
	if ngName == "ktelexus" || ngName == "ktelexgb" {
		return "ktelex"
	}
	return ngName
}

// isEnEngine reports whether engineName is an EN engine, which shares the
// per-app input mode mapping of the master (VI) config.
func isEnEngine(engineName string) bool {
	return engineName == "ktelexus" || engineName == "ktelexgb"
}

// LoadConfig returns the config of engineName. EN engines use the master's
// per-app input mode mapping; never save such a copy back to the EN file
// (use UpdateConfig to change a file).
func LoadConfig(engineName string) *Config {
	c := loadOwnConfig(engineName)
	if isEnEngine(engineName) {
		// Every writer stores the mapping in the master file; entries in
		// the EN file are leftovers of an old bug that saved merged copies.
		// The settings GUI edits the master only: hotkeys and "disable on
		// games" come from there too.
		m := loadOwnConfig(MasterName(engineName))
		c.InputModeMapping = m.InputModeMapping
		c.Shortcuts = m.Shortcuts
		c.IBflags = c.IBflags&^IBdisableOnGame | m.IBflags&IBdisableOnGame
	}
	return c
}

// loadOwnConfig reads the file of engineName alone. A file that does not
// parse is copied aside (".broken") and defaults are used, so a later save
// does not silently destroy what the user wrote.
func loadOwnConfig(engineName string) *Config {
	var c = DefaultCfg(engineName)
	path := GetConfigPath(engineName)
	data, err := ioutil.ReadFile(path)
	if err == nil {
		if perr := json.Unmarshal(data, &c); perr != nil {
			log.Printf("config %s: parse error: %v (using defaults)", path, perr)
			backupBrokenFile(path, data)
			c = DefaultCfg(engineName)
			err = perr
		}
	}
	c.InputMethodDefinitions = bamboo.GetInputMethodDefinitions()
	if c.InputModeMapping == nil {
		c.InputModeMapping = map[string]int{}
	}
	if err == nil && isOldFormat(data) {
		SaveConfig(&c, engineName)
	}
	return &c
}

func backupBrokenFile(path string, data []byte) {
	bak := path + ".broken"
	if old, err := ioutil.ReadFile(bak); err == nil && string(old) == string(data) {
		return
	}
	if err := WriteFileAtomic(bak, data, 0644); err != nil {
		log.Printf("config: cannot back up %s: %v", path, err)
	} else {
		log.Printf("config: unreadable file saved as %s", bak)
	}
}

// UpdateConfig loads the file of engineName (without the EN mapping
// merge), lets fn change it and saves it, holding a lock shared with the
// settings GUI so concurrent read-modify-write cycles do not lose updates.
func UpdateConfig(engineName string, fn func(c *Config)) error {
	unlock := lockConfigDir(engineName)
	defer unlock()
	c := loadOwnConfig(engineName)
	fn(c)
	return SaveConfig(c, engineName)
}

func lockConfigDir(engineName string) func() {
	dir := GetConfigDir(engineName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return func() {}
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return func() {}
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return func() {}
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}

// WriteFileAtomic writes data to a temp file in the same directory, syncs
// it and renames it over path, so readers never see a half-written file.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := ioutil.TempFile(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// isOldFormat checks whether the raw JSON uses the old opaque-bitfield format.
func isOldFormat(data []byte) bool {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return false
	}
	_, hasIBflags := raw["IBflags"]
	_, hasOldShortcuts := raw["Shortcuts"]
	return hasIBflags || hasOldShortcuts
}

func SaveConfig(c *Config, engineName string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		log.Println("SaveConfig marshal:", err)
		return err
	}
	if err := WriteFileAtomic(GetConfigPath(engineName), data, 0644); err != nil {
		log.Println("SaveConfig write:", err)
		return err
	}
	return nil
}
