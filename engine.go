/*
 * Bamboo - A Vietnamese Input method editor
 * Copyright (C) 2018 Luong Thanh Lam <ltlam93@gmail.com>
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package main

import (
	"fmt"
	"log"
	"os/exec"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/BambooEngine/bamboo-core"
	ibus "github.com/BambooEngine/goibus"
	"github.com/godbus/dbus/v5"

	"ktelex/config"
)

type Engine struct {
	sync.Mutex
	IEngine
	preeditor            bamboo.IEngine
	engineName           string
	config               *config.Config
	propList             *ibus.PropList
	respectAppMapping    bool // EN engine ignores per-app InputModeMapping
	englishMode          bool
	macroTable           *MacroTable
	wmClasses            string
	isInputModeLTOpened  bool
	isEmojiLTOpened      bool
	emojiLookupTable     *ibus.LookupTable
	inputModeLookupTable *ibus.LookupTable
	capabilities         uint32
	keyPressDelay        int
	isFirstTimeSendingBS bool
	// Uinput BS counting + deferred commit (mirrors skey's approach)
	uinputDeleting_         bool        // true while waiting for BS echoes
	uinputExpectingBs_      int         // BS echoes we're waiting for
	uinputSeenBs_           int         // BS echoes counted so far
	uinputOwedBs_           int         // BS echoes still owed when tx completed early (debt counter)
	uinputOwedExpiresAt_    time.Time   // expiry for owed BS echo debt
	uinputPendingCommit_    string      // text to commit after all BS done
	uinputDeferredKeys_     []uint32    // buffered key vals during pending BS
	uinputDeferredCodes_    []uint32    // buffered key codes
	uinputDeferredStates_   []uint32    // buffered key states
	uinputCommitTimer_      *time.Timer // async commit timer (skey-style)
	uinputBsSentAt_         time.Time   // when BS was sent via uinput (for EWMA)
	uinputBsRtEwmaUsec_     int64       // EWMA of BS round-trip in µs
	uinputOwedKey_          uint32      // keyval of the owed echoes (Backspace or Left)
	uinputTxWm_             string      // app the active transaction was opened in
	uinputTxSelect_         bool        // active tx removes text via Shift+Left selection
	uinputFocused_          bool        // text input currently focused (FocusIn/FocusOut)
	uinputFocusOutAt_       time.Time   // last FocusOut
	uinputAwaitFocus_       bool        // commit is held until focus returns
	uinputLastKeyAt_        time.Time   // last real key event in UinputIM
	uinputLastWm_           string      // wmClass that "owns" the current preedit
	uinputTxID_             uint64      // generation counter for the active transaction
	uinputTxEndAt_          time.Time   // when the last transaction ended (stray-BS guard window)
	uinputLastIdleBsAt_     time.Time   // timestamp of last idle backspace received
	uinputIdleBsBurstCount_ int         // consecutive rapid backspaces received while idle (runaway guard)
	emoji                   *EmojiEngine
	isSurroundingTextReady  bool
	lastKeyWithShift        bool
	lastCommitText          int64
	// restore key strokes by pressing Shift + Space
	shouldRestoreKeyStrokes bool
	// enqueue key strokes to process later
	shouldEnqueuKeyStrokes  bool
	isGame                  bool         // cached: current focused app is a game
	inputPurpose_           uint32       // IBusInputPurpose of the focused field (guarded by Mutex)
	uinputSurroundSeen_     bool         // client sent usable surrounding text since FocusIn (UinputIM)
	uinputInlineCompletion_ bool         // focused field showed an inline completion (surrounding text)
	uinputSurrBefore_       string       // text before the caret in the last surrounding text (no selection)
	uinputSurrValid_        bool         // uinputSurrBefore_ describes the field (caret, no selection)
	uinputDstCheck_         *dstCheck    // pending self-check of the last surrounding-text replace
	configMtime_            time.Time    // newest mtime of the config files when last loaded
	configGen_              uint64       // bumped on every config change (atomic)
	imCacheMu               sync.Mutex   // guards imCache
	preeditMu               sync.Mutex   // serialises Preedit-mode keys vs. Reset/FocusOut
	imCache                 inputModeKey // last getInputMode() inputs
	imCacheMode             int          // and its result
}

func NewIbusBambooEngine(name string, cfg *config.Config, base IEngine, preeditor bamboo.IEngine) *Engine {
	return &Engine{
		engineName:        name,
		IEngine:           base,
		preeditor:         preeditor,
		config:            cfg,
		respectAppMapping: name != "ktelexus" && name != "ktelexgb",
	}
}

/*
*
Implement IBus.Engine's process_key_event default signal handler.

Args:

	keyval - The keycode, transformed through a keymap, stays the
		same for every keyboard
	keycode - Keyboard-dependant key code
	modifiers - The state of IBus.ModifierType keys like
		Shift, Control, etc.

Return:

	True - if successfully process the keyevent
	False - otherwise. The keyevent will be passed to X-Client

This function gets called whenever a key is pressed.
*/
func (e *Engine) ProcessKeyEvent(keyVal uint32, keyCode uint32, state uint32) (bool, *dbus.Error) {
	if state&IBusReleaseMask != 0 {
		return false, nil
	}
	ticket := keyOrder.acquire(keyTriple{keyVal, keyCode, state})
	defer keyOrder.release(ticket)

	// Password/PIN fields: never transform, inject or log anything.
	if e.isSecretInput() {
		return false, nil
	}
	debugf(">>>>ProcessKeyEvent > %d | state %d keyVal 0x%04x | %c", len(keyPressChan), state, keyVal, rune(keyVal))

	// EN engine: pure passthrough. Only Alt+Z opens popup to configure VI engine.
	if e.engineName == "ktelexus" || e.engineName == "ktelexgb" {
		if e.isInputModeLTOpened {
			_, ret := e.ltProcessKeyEvent(keyVal, keyCode, state)
			return ret, nil
		}
		if e.isShortcutKeyPressed(keyVal, state, KSInputModeSwitch) && e.getWmClass() != "" {
			e.resetBuffer()
			e.isInputModeLTOpened = true
			e.lastKeyWithShift = true
			currentMode, hasMapping := e.config.InputModeMapping[e.getWmClass()]
			if !hasMapping {
				currentMode = -1
			}
			e.openLookupTableWithMode(currentMode)
			return true, nil
		}
		return false, nil
	}

	if ret, retValue := e.processShortcutKey(keyVal, keyCode, state); ret {
		return retValue, nil
	}

	// Fast-path: In games or when buffer is empty, immediately forward Enter key (IBusReturn / IBusKP_Enter)
	// to avoid focus-grab race conditions in game chat inputs.
	if keyVal == IBusReturn || keyVal == IBusKP_Enter {
		if e.isGame || e.getRawKeyLen() == 0 {
			if e.getRawKeyLen() > 0 {
				e.resetBuffer()
			}
			return false, nil
		}
	}
	if e.inBackspaceWhiteList() {
		return e.bsProcessKeyEvent(keyVal, keyCode, state)
	}
	if e.checkInputMode(config.UinputIM) {
		return e.uinputProcessKeyEvent(keyVal, keyCode, state)
	}
	e.preeditMu.Lock()
	defer e.preeditMu.Unlock()
	return e.preeditProcessKeyEvent(keyVal, keyCode, state)
}

func (e *Engine) FocusIn() *dbus.Error {
	noteEngineActive(e.engineName)
	e.reloadConfigIfChanged()
	var latestWm = e.getLatestWmClass()
	debugf("FocusIn: %s", latestWm)
	// A focus flap inside the same app must not end an open transaction:
	// committing now would overtake the deletion still queued in the
	// compositor. See uinputOnFocusIn.
	if e.checkInputMode(config.UinputIM) {
		e.uinputOnFocusIn(latestWm)
	}
	e.checkWmClass(latestWm)
	if e.config.IBflags&config.IBdisableOnGame != 0 {
		// Empty WM_CLASS shows up on transient flaps: keep the last verdict.
		if latestWm != "" {
			e.isGame = isGameWindow(latestWm)
		}
	} else {
		e.isGame = false
	}
	e.RegisterProperties(e.propList)
	e.RequireSurroundingText()
	if e.isShortcutKeyEnable(KSEmojiDialog) {
		ensureEmojiTrie()
	}
	if e.config.IBflags&config.IBspellCheckWithDicts != 0 {
		ensureDictionary()
	}
	return nil
}

func (e *Engine) FocusOut() *dbus.Error {
	debugf("FocusOut.")
	if e.checkInputMode(config.UinputIM) {
		// Browsers/Electron flap FocusOut/FocusIn on DOM edits: keep the
		// syllable and any open transaction (FocusIn decides), only record
		// the focus state so a commit is not sent into an unfocused input.
		e.uinputOnFocusOut()
		return nil
	}
	// Commit pending preedit to the losing window now (Bamboo PR #611);
	// otherwise the uncommitted composing text is dropped or leaks into the next window.
	e.preeditMu.Lock()
	e.resetBuffer()
	e.preeditMu.Unlock()
	if e.isEmojiLTOpened {
		e.closeEmojiCandidates()
	}
	if e.isInputModeLTOpened {
		e.closeInputModeCandidates()
	}
	return nil
}

func (e *Engine) Reset() *dbus.Error {
	debugf("Reset.")
	if e.checkInputMode(config.PreeditIM) {
		e.preeditMu.Lock()
		e.preeditor.Reset()
		e.preeditMu.Unlock()
	} else if e.checkInputMode(config.UinputIM) {
		e.uinputOnReset()
	}
	return nil
}

func (e *Engine) Enable() *dbus.Error {
	log.Printf("Enable %s", e.engineName)
	noteEngineActive(e.engineName)
	e.preeditor.Reset()
	e.RequireSurroundingText()
	return nil
}

func (e *Engine) Disable() *dbus.Error {
	fmt.Print("Disable.\n")
	e.Lock()
	// Switching away mid-transaction: the deletion is already on its way,
	// so commit the replacement now rather than leaving the word truncated.
	pending := ""
	if e.uinputDeleting_ {
		pending = e.uinputPendingCommit_
	}
	e.uinputResetTxStateLocked()
	if pending != "" {
		e.commitText(pending)
	}
	e.preeditor.Reset()
	e.Unlock()
	return nil
}

// @method(in_signature="vuu")
func (e *Engine) SetSurroundingText(text dbus.Variant, cursorPos uint32, anchorPos uint32) *dbus.Error {
	if e.checkInputMode(config.UinputIM) {
		if s, ok := ibusTextString(text); ok {
			e.uinputOnSurroundingText(s, cursorPos, anchorPos)
		}
		return nil
	}
	if !e.isSurroundingTextReady {
		//fmt.Println("Surrounding Text is not ready yet.")
		return nil
	}
	e.Lock()
	defer func() {
		e.Unlock()
		e.isSurroundingTextReady = false
		if err := recover(); err != nil {
			fmt.Println(err)
		}
	}()
	if e.inBackspaceWhiteList() {
		var str = reflect.ValueOf(reflect.ValueOf(text.Value()).Index(2).Interface()).String()
		var s = []rune(str)
		if len(s) < int(cursorPos) {
			return nil
		}
		var cs = s[:cursorPos]
		debugf("Surrounding Text: %q", string(cs))
		e.preeditor.Reset()
		for i := len(cs) - 1; i >= 0; i-- {
			// workaround for spell checking
			if bamboo.IsPunctuationMark(cs[i]) && e.preeditor.CanProcessKey(cs[i]) {
				cs[i] = ' '
			}
			e.preeditor.ProcessKey(cs[i], bamboo.EnglishMode|bamboo.InReverseOrder)
		}
	}
	return nil
}

func (e *Engine) PageUp() *dbus.Error {
	if e.isEmojiLTOpened && e.emojiLookupTable.PageUp() {
		e.updateEmojiLookupTable()
	}
	if e.isInputModeLTOpened && e.inputModeLookupTable.PageUp() {
		e.updateInputModeLT()
	}
	return nil
}

func (e *Engine) PageDown() *dbus.Error {
	if e.isEmojiLTOpened && e.emojiLookupTable.PageDown() {
		e.updateEmojiLookupTable()
	}
	if e.isInputModeLTOpened && e.inputModeLookupTable.PageDown() {
		e.updateInputModeLT()
	}
	return nil
}

func (e *Engine) CursorUp() *dbus.Error {
	if e.isEmojiLTOpened && e.emojiLookupTable.CursorUp() {
		e.updateEmojiLookupTable()
	}
	if e.isInputModeLTOpened && e.inputModeLookupTable.CursorUp() {
		e.updateInputModeLT()
	}
	return nil
}

func (e *Engine) CursorDown() *dbus.Error {
	if e.isEmojiLTOpened && e.emojiLookupTable.CursorDown() {
		e.updateEmojiLookupTable()
	}
	if e.isInputModeLTOpened && e.inputModeLookupTable.CursorDown() {
		e.updateInputModeLT()
	}
	return nil
}

func (e *Engine) CandidateClicked(index uint32, button uint32, state uint32) *dbus.Error {
	if e.isEmojiLTOpened && e.updateCursorPosInEmojiTable(index) {
		e.commitEmojiCandidate()
		e.closeEmojiCandidates()
	}
	if e.isInputModeLTOpened && e.inputModeLookupTable.SetCursorPos(index) {
		e.commitInputModeCandidate()
		e.closeInputModeCandidates()
	}
	return nil
}

func (e *Engine) SetCapabilities(cap uint32) *dbus.Error {
	e.capabilities = cap
	return nil
}

func (e *Engine) SetCursorLocation(x int32, y int32, w int32, h int32) *dbus.Error {
	return nil
}

// IBusInputPurpose values (ibustypes.h).
const (
	ibusPurposeUrl      = 5
	ibusPurposePassword = 8
	ibusPurposePin      = 9
)

func (e *Engine) SetContentType(purpose uint32, hints uint32) *dbus.Error {
	e.Lock()
	wasSecret := e.inputPurpose_ == ibusPurposePassword || e.inputPurpose_ == ibusPurposePin
	e.inputPurpose_ = purpose
	isSecret := purpose == ibusPurposePassword || purpose == ibusPurposePin
	if isSecret && !wasSecret {
		// Entering a password field: drop the syllable typed elsewhere.
		e.uinputResetTxStateLocked()
		e.preeditor.Reset()
	}
	e.Unlock()
	return nil
}

func (e *Engine) isSecretInput() bool {
	e.Lock()
	defer e.Unlock()
	return e.inputPurpose_ == ibusPurposePassword || e.inputPurpose_ == ibusPurposePin
}

// @method(in_signature="su")
func (e *Engine) PropertyActivate(propName string, propState uint32) *dbus.Error {
	if propName == PropKeyAbout {
		exec.Command("xdg-open", HomePage).Start()
		return nil
	}
	if propName == PropKeyConfiguration || propName == PropKeyInputModeLookupTableShortcut || propName == PropKeyMacroTable {
		// Non-blocking: the window lives in its own process; the saved
		// config is picked up when it closes.
		openSettingsAsync(func() { e.reloadConfigIfChanged() })
		return nil
	}

	// Key handling, transaction timers and FocusIn reloads all touch
	// config and preeditor: change them under the engine lock.
	e.Lock()
	defer e.Unlock()
	oldInputMethod, oldFlags := e.config.InputMethod, e.config.Flags

	turnSpellChecking := func(on bool) {
		if on {
			e.config.IBflags |= config.IBspellCheckEnabled
			e.config.IBflags |= config.IBautoNonVnRestore
			if e.config.IBflags&config.IBspellCheckWithDicts == 0 {
				e.config.IBflags |= config.IBspellCheckWithRules
			}
		} else {
			e.config.IBflags &= ^config.IBspellCheckEnabled
			e.config.IBflags &= ^config.IBautoNonVnRestore
		}
	}

	if propName == PropKeyStdToneStyle {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.Flags |= bamboo.EstdToneStyle
		} else {
			e.config.Flags &= ^bamboo.EstdToneStyle
		}
	}
	if propName == PropKeyFreeToneMarking {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.Flags |= bamboo.EfreeToneMarking
		} else {
			e.config.Flags &= ^bamboo.EfreeToneMarking
		}
	}
	if propName == PropKeyEnableSpellCheck {
		if propState == ibus.PROP_STATE_CHECKED {
			turnSpellChecking(true)
		} else {
			turnSpellChecking(false)
		}
	}
	if propName == PropKeySpellCheckByRules {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= config.IBspellCheckWithRules
			e.config.IBflags &= ^config.IBspellCheckWithDicts
			turnSpellChecking(true)
		} else {
			e.config.IBflags &= ^config.IBspellCheckWithRules
		}
	}
	if propName == PropKeySpellCheckByDicts {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= config.IBspellCheckWithDicts
			e.config.IBflags &= ^config.IBspellCheckWithRules
			turnSpellChecking(true)
			ensureDictionary()
		} else {
			e.config.IBflags &= ^config.IBspellCheckWithDicts
		}
	}
	if propName == PropKeyMacroEnabled {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= config.IBmacroEnabled
			e.macroTable.Enable(e.engineName)
		} else {
			e.config.IBflags &= ^config.IBmacroEnabled
			e.macroTable.Disable()
		}
	}
	if propName == PropKeyDisableOnGame {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= config.IBdisableOnGame
			wm := e.getWmClass()
			if isGameByWmClass(wm) {
				e.isGame = true
				log.Printf("[gameDetect] input disabled by WM_CLASS %q", wm)
			} else {
				pid := getFocusedPID(wm)
				e.isGame = isGameProcess(pid)
				if e.isGame {
					log.Printf("[gameDetect] input disabled for current PID %d (%s)", pid, wm)
				}
			}
		} else {
			e.config.IBflags &= ^config.IBdisableOnGame
			e.isGame = false
		}
	}
	if propName == PropKeyAutoCapitalizeMacro {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= config.IBautoCapitalizeMacro
		} else {
			e.config.IBflags &= ^config.IBautoCapitalizeMacro
		}
		if e.config.IBflags&config.IBmacroEnabled != 0 {
			e.macroTable.Reload(e.engineName, e.config.IBflags&config.IBautoCapitalizeMacro != 0)
		}
	}

	var im, foundIm = getValueFromPropKey(propName, "InputMode")
	if foundIm && propState == ibus.PROP_STATE_CHECKED {
		e.config.DefaultInputMode, _ = strconv.Atoi(im)
	}
	var charset, foundCs = getValueFromPropKey(propName, "OutputCharset")
	if foundCs && isValidCharset(charset) && propState == ibus.PROP_STATE_CHECKED {
		e.config.OutputCharset = charset
	}
	if _, found := e.config.InputMethodDefinitions[propName]; found && propState == ibus.PROP_STATE_CHECKED {
		e.config.InputMethod = propName
	}
	if propName != "-" {
		config.SaveConfig(e.config, e.engineName)
	}
	e.propList = GetPropListByConfig(e.config)

	if e.config.InputMethod != oldInputMethod || e.config.Flags != oldFlags {
		var inputMethod = bamboo.ParseInputMethod(e.config.InputMethodDefinitions, e.config.InputMethod)
		if inputMethod.Name == "" {
			inputMethod = bamboo.ParseInputMethod(e.config.InputMethodDefinitions, bamboo.DefaultInputMethodName)
			e.config.InputMethod = bamboo.DefaultInputMethodName
		}
		e.uinputResetTxStateLocked()
		e.preeditor = bamboo.NewEngine(inputMethod, e.config.Flags)
	}
	e.configMtime_ = configFilesMtime(e.engineName)
	e.bumpConfigGen()
	e.RegisterProperties(e.propList)
	return nil
}
