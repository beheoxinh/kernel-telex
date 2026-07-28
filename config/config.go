package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os/user"
	"strings"

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
}

// configJSON is the human-readable JSON surface.
type configJSON struct {
	InputMethod   string `json:"inputMethod"`
	OutputCharset string `json:"outputCharset"`

	// ── bamboo-core Flags ──
	FreeToneMarking  bool `json:"freeToneMarking"`
	StdToneStyle     bool `json:"stdToneStyle"`
	AutoCorrect      bool `json:"autoCorrect"`

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
	InputModeSwitch   string `json:"inputModeSwitch,omitempty"`
	RestoreKeystrokes string `json:"restoreKeystrokes,omitempty"`
	ViEnSwitch        string `json:"viEnSwitch,omitempty"`
	EmojiDialog       string `json:"emojiDialog,omitempty"`

	DefaultInputMode int            `json:"defaultInputMode"`
	InputModeMapping map[string]int `json:"inputModeMapping,omitempty"`
}

func (c *Config) MarshalJSON() ([]byte, error) {
	j := configJSON{
		InputMethod:   c.InputMethod,
		OutputCharset: c.OutputCharset,
		FreeToneMarking:        c.Flags&bamboo.EfreeToneMarking != 0,
		StdToneStyle:           c.Flags&bamboo.EstdToneStyle != 0,
		AutoCorrect:            c.Flags&bamboo.EautoCorrectEnabled != 0,
		UseMacro:               c.IBflags&IBmacroEnabled != 0,
		AutoCapitalizeMacro:    c.IBflags&IBautoCapitalizeMacro != 0,
		SpellCheck:             c.IBflags&IBspellCheckEnabled != 0,
		SpellCheckByRules:      c.IBflags&IBspellCheckWithRules != 0,
		SpellCheckByDicts:      c.IBflags&IBspellCheckWithDicts != 0,
		AutoNonVnRestore:       c.IBflags&IBautoNonVnRestore != 0,
		DdFreeStyle:            c.IBflags&IBddFreeStyle != 0,
		DisableOnGame:          c.IBflags&IBdisableOnGame != 0,
		InputModeSwitch:        shortcutString(c.Shortcuts[0], c.Shortcuts[1]),
		RestoreKeystrokes:      shortcutString(c.Shortcuts[2], c.Shortcuts[3]),
		ViEnSwitch:             shortcutString(c.Shortcuts[4], c.Shortcuts[5]),
		EmojiDialog:            shortcutString(c.Shortcuts[6], c.Shortcuts[7]),
		DefaultInputMode:       c.DefaultInputMode,
		InputModeMapping:       c.InputModeMapping,
	}
	return json.Marshal(j)
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

	var j configJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	c.InputMethod = j.InputMethod
	c.OutputCharset = j.OutputCharset
	c.DefaultInputMode = j.DefaultInputMode
	c.InputModeMapping = j.InputModeMapping

	c.IBflags = 0
	if j.UseMacro {
		c.IBflags |= IBmacroEnabled
	}
	if j.AutoCapitalizeMacro {
		c.IBflags |= IBautoCapitalizeMacro
	}
	if j.SpellCheck {
		c.IBflags |= IBspellCheckEnabled
		// Backward compat: old configs had autoNonVnRestore tied to spellCheck
		if !hasAutoNonVn {
			c.IBflags |= IBautoNonVnRestore
		}
	}
	if j.AutoNonVnRestore {
		c.IBflags |= IBautoNonVnRestore
	}
	if j.DdFreeStyle {
		c.IBflags |= IBddFreeStyle
	}
	// WPS workaround always enabled
	c.IBflags |= IBworkaroundForWPS
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

var keyNames = map[string]uint32{
	"0": 0x30, "1": 0x31, "2": 0x32, "3": 0x33, "4": 0x34,
	"5": 0x35, "6": 0x36, "7": 0x37, "8": 0x38, "9": 0x39,
	"A": 0x41, "B": 0x42, "C": 0x43, "D": 0x44, "E": 0x45,
	"F": 0x46, "G": 0x47, "H": 0x48, "I": 0x49, "J": 0x4A,
	"K": 0x4B, "L": 0x4C, "M": 0x4D, "N": 0x4E, "O": 0x4F,
	"P": 0x50, "Q": 0x51, "R": 0x52, "S": 0x53, "T": 0x54,
	"U": 0x55, "V": 0x56, "W": 0x57, "X": 0x58, "Y": 0x59,
	"Z": 0x5A, "~": 0x7E, ".": 0x2E, ",": 0x2C,
}

var revKeyNames map[uint32]string

func init() {
	revKeyNames = make(map[uint32]string, len(keyNames)*2)
	for k, v := range keyNames {
		revKeyNames[v] = k
		// Map lowercase keyval -> uppercase name (old-format stores lowercase keyvals)
		if len(k) == 1 && k[0] >= 'A' && k[0] <= 'Z' {
			revKeyNames[v+32] = k
		}
	}
}

func parseShortcut(s string, mask, keyval *uint32) {
	*mask = 0
	*keyval = 0
	if s == "" {
		return
	}
	parts := strings.Split(s, "+")
	for i, p := range parts {
		switch p {
		case "Ctrl":
			*mask |= 4
		case "Alt":
			*mask |= 8
		case "Shift":
			*mask |= 1
		case "Super":
			*mask |= 64
		default:
			if i == len(parts)-1 {
				if kv, ok := keyNames[strings.ToUpper(p)]; ok {
					// C code stores lowercase keyvals (gdk_keyval_to_lower)
					if kv >= 0x41 && kv <= 0x5A {
						*keyval = kv + 32
					} else {
						*keyval = kv
					}
				} else if len(p) == 1 {
					kv := uint32(strings.ToUpper(p)[0])
					if kv >= 0x30 && kv <= 0x5A {
						if kv >= 0x41 {
							*keyval = kv + 32
						} else {
							*keyval = kv
						}
					}
				}
			}
		}
	}
}

func shortcutString(mask, keyval uint32) string {
	if mask == 0 && keyval == 0 {
		return ""
	}
	var parts []string
	if mask&4 != 0 {
		parts = append(parts, "Ctrl")
	}
	if mask&8 != 0 {
		parts = append(parts, "Alt")
	}
	if mask&1 != 0 {
		parts = append(parts, "Shift")
	}
	if mask&64 != 0 {
		parts = append(parts, "Super")
	}
	if keyval > 0 {
		if name, ok := revKeyNames[keyval]; ok {
			if keyval >= 0x41 && keyval <= 0x5A {
				parts = append(parts, strings.ToUpper(name))
			} else {
				parts = append(parts, name)
			}
		} else if keyval >= 0x30 && keyval <= 0x39 {
			parts = append(parts, string(rune(keyval)))
		} else {
			parts = append(parts, fmt.Sprintf("0x%04X", keyval))
		}
	}
	return strings.Join(parts, "+")
}

// ── Path helpers ──────────────────────────────────────────────────

func GetConfigDir(ngName string) string {
	u, err := user.Current()
	if err == nil {
		return fmt.Sprintf(configDir, u.HomeDir, "ktelex")
	}
	return fmt.Sprintf(configDir, "~", "ktelex")
}

func GetMacroPath(engineName string) string {
	// Macro file is shared between VI and EN engines
	master := engineName
	if engineName == "ktelexus" || engineName == "ktelexgb" {
		master = "ktelex"
	}
	return fmt.Sprintf(mactabFile, GetConfigDir("ktelex"), master)
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
		InputModeMapping:       map[string]int{},
	}
}

func MasterName(ngName string) string {
	if ngName == "ktelexus" || ngName == "ktelexgb" {
		return "ktelex"
	}
	return ngName
}

func LoadConfig(engineName string) *Config {
	var c = DefaultCfg(engineName)

	data, err := ioutil.ReadFile(GetConfigPath(engineName))
	if err == nil {
		json.Unmarshal(data, &c)
	}
	c.InputMethodDefinitions = bamboo.GetInputMethodDefinitions()

	if err == nil && isOldFormat(data) {
		SaveConfig(&c, engineName)
	}

	// EN engine: merge InputModeMapping from VI engine's config (shared mappings)
	if engineName == "ktelexus" || engineName == "ktelexgb" {
		viData, viErr := ioutil.ReadFile(GetConfigPath("ktelex"))
		if viErr == nil {
			var viCfg Config
			if json.Unmarshal(viData, &viCfg) == nil {
				if c.InputModeMapping == nil {
					c.InputModeMapping = map[string]int{}
				}
				for app, mode := range viCfg.InputModeMapping {
					c.InputModeMapping[app] = mode
				}
			}
		}
	}

	return &c
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

func SaveConfig(c *Config, engineName string) {
	fn := fmt.Sprintf(configFile, GetConfigDir(engineName), engineName)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		log.Println("SaveConfig marshal:", err)
		return
	}
	if err := ioutil.WriteFile(fn, data, 0644); err != nil {
		log.Println("SaveConfig write:", err)
	}
}
