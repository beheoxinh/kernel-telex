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
	"log"
	"os/exec"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
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
	propsShown_          *ibus.PropList // last list sent by FocusIn (see focusPropsOwner)
	respectAppMapping    bool           // EN engine ignores per-app InputModeMapping
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
	uinputDstAt_            time.Time   // when the last surrounding-text replace was sent
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
	uinputSurrSel_          bool         // the last surrounding text had a selection
	uinputSurrAny_          bool         // the app reported surrounding text since FocusIn (even empty)
	uinputAwait_            *dstAwait    // replace waiting for fresh surrounding text (fast typing)
	configMtime_            time.Time    // newest mtime of the config files when last loaded
	configGen_              uint64       // bumped on every config change (atomic)
	imCacheMu               sync.Mutex   // guards imCache
	wmMu                    sync.Mutex   // guards wmClasses and isGame (read per key, also outside the engine lock)
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
func (e *Engine) ProcessKeyEvent(keyVal uint32, keyCode uint32, state uint32) (handled bool, dbusErr *dbus.Error) {
	// godbus runs each call in its own goroutine: a panic here would kill the
	// whole engine process. Pass the key through and start clean instead.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ProcessKeyEvent: recovered from panic: %v", r)
			if e.TryLock() {
				e.uinputResetTxStateLocked()
				e.recoverResetComposerLocked()
				e.Unlock()
			}
			handled, dbusErr = false, nil
		}
	}()
	if state&IBusReleaseMask != 0 {
		return false, nil
	}
	ticket := keyOrder.acquire(keyTriple{keyVal, keyCode, state})
	defer keyOrder.release(ticket)
	// Text this key commits must not reach the app before the previous
	// key's reply (a passed-through character) does: wait until it is out.
	replyOrder.waitBefore(keyOrder.replySeq(ticket))

	// Keys without a keysym (e.g. the click-to-unstick KEY_UNKNOWN tap)
	// carry no text: pass them through untouched.
	if keyVal == 0 || keyVal == IBusVoidSymbol {
		return false, nil
	}
	// Caps Lock and the layout group keys belong to the system: with
	// grp:caps_toggle Caps Lock switches the input source (VI <-> EN).
	// Never consume or reorder them.
	if config.IsLockOrGroupKey(keyVal) {
		return false, nil
	}
	if unstickRiskKey(keyVal) {
		unstickNoteRisk()
	}

	// Password/PIN fields: never transform, inject or log anything.
	if e.isSecretInput() {
		return false, nil
	}
	debugf(">>>>ProcessKeyEvent > %d | state %d keyVal 0x%04x | %c", atomic.LoadInt32(&surroundInflight), state, keyVal, rune(keyVal))

	// Everything below reads or changes the composer, the lookup tables and
	// the config, which FocusIn, timers and menu actions change too: hold the
	// engine lock. The uinput and surrounding-text paths take it themselves.
	switch path, ret := e.processKeyFront(keyVal, keyCode, state); path {
	case keyPathSurround:
		return e.bsProcessKeyEvent(keyVal, keyCode, state)
	case keyPathUinput:
		return e.uinputProcessKeyEvent(keyVal, keyCode, state)
	default:
		return ret, nil
	}
}

// focusPropsOwner is the engine whose property list the panel shows.
var focusPropsOwner atomic.Pointer[Engine]

type keyPath int

const (
	keyPathDone keyPath = iota // handled (Preedit mode, shortcuts, EN engine)
	keyPathSurround
	keyPathUinput
)

// processKeyFront runs the shared prelude and the Preedit path under the
// engine lock; the uinput and surrounding-text paths take it themselves.
func (e *Engine) processKeyFront(keyVal, keyCode, state uint32) (path keyPath, ret bool) {
	e.Lock()
	defer e.Unlock()
	defer e.recoverKeyPanicLocked("key")
	if done, r := e.keyPreludeLocked(keyVal, keyCode, state); done {
		return keyPathDone, r
	}
	if e.inBackspaceWhiteList() {
		return keyPathSurround, false
	}
	if e.checkInputMode(config.UinputIM) {
		return keyPathUinput, false
	}
	r, _ := e.preeditProcessKeyEvent(keyVal, keyCode, state)
	return keyPathDone, r
}

// keyPreludeLocked handles what comes before the input-mode specific paths:
// the EN engine, shortcuts, lookup tables and the Enter fast path.
func (e *Engine) keyPreludeLocked(keyVal, keyCode, state uint32) (done, ret bool) {
	// EN engine: pure passthrough. Only Alt+Z opens popup to configure VI engine.
	if e.engineName == "ktelexus" || e.engineName == "ktelexgb" {
		if e.isInputModeLTOpened {
			_, ret := e.ltProcessKeyEvent(keyVal, keyCode, state)
			return true, ret
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
			return true, true
		}
		return true, false
	}

	if ret, retValue := e.processShortcutKey(keyVal, keyCode, state); ret {
		return true, retValue
	}

	// Fast-path: In games or when buffer is empty, immediately forward Enter key (IBusReturn / IBusKP_Enter)
	// to avoid focus-grab race conditions in game chat inputs.
	if keyVal == IBusReturn || keyVal == IBusKP_Enter {
		if e.isGameNow() || e.getRawKeyLen() == 0 {
			if e.getRawKeyLen() > 0 && !e.uinputTxOpenLocked() {
				e.resetBuffer()
			}
			return true, false
		}
	}
	return false, false
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
	game, setGame := false, true
	if e.config.IBflags&config.IBdisableOnGame != 0 {
		// Empty WM_CLASS shows up on transient flaps: keep the last verdict.
		if latestWm != "" {
			game = isGameWindow(latestWm) // may read /proc: outside the lock
		} else {
			setGame = false
		}
	}
	e.Lock()
	e.checkWmClass(latestWm)
	if setGame {
		e.setGame(game)
	}
	props := e.propList
	// Apps such as ONLYOFFICE flap FocusOut/FocusIn many times a second.
	// The panel still shows this engine's list if no other engine took the
	// focus since and the list did not change: skip the D-Bus signal then.
	resend := focusPropsOwner.Swap(e) != e || e.propsShown_ != props
	e.propsShown_ = props
	e.Unlock()
	if resend {
		e.RegisterProperties(props)
	}
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
	e.Lock()
	defer e.Unlock()
	e.resetBuffer()
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
		e.Lock()
		e.preeditor.Reset()
		e.Unlock()
	} else if e.checkInputMode(config.UinputIM) {
		e.uinputOnReset()
	} else if e.checkInputMode(config.SurroundingTextIM) {
		// The caret moved (click, app edit): forget the syllable and rebuild
		// it from the next surrounding text, or the next tone key would
		// delete characters at the new position.
		e.Lock()
		e.preeditor.Reset()
		e.isSurroundingTextReady = true
		e.Unlock()
	}
	return nil
}

func (e *Engine) Enable() *dbus.Error {
	log.Printf("Enable %s", e.engineName)
	noteEngineActive(e.engineName)
	focusPropsOwner.Store(nil)
	e.Lock()
	e.preeditor.Reset()
	e.Unlock()
	e.RequireSurroundingText()
	return nil
}

func (e *Engine) Disable() *dbus.Error {
	debugf("Disable %s", e.engineName)
	focusPropsOwner.Store(nil)
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
	e.Lock()
	defer func() {
		e.isSurroundingTextReady = false
		e.Unlock()
		if err := recover(); err != nil {
			log.Printf("SetSurroundingText: recovered: %v", err)
		}
	}()
	if !e.isSurroundingTextReady {
		return nil
	}
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
	e.Lock()
	defer e.Unlock()
	if e.isEmojiLTOpened && e.emojiLookupTable.PageUp() {
		e.updateEmojiLookupTable()
	}
	if e.isInputModeLTOpened && e.inputModeLookupTable.PageUp() {
		e.updateInputModeLT()
	}
	return nil
}

func (e *Engine) PageDown() *dbus.Error {
	e.Lock()
	defer e.Unlock()
	if e.isEmojiLTOpened && e.emojiLookupTable.PageDown() {
		e.updateEmojiLookupTable()
	}
	if e.isInputModeLTOpened && e.inputModeLookupTable.PageDown() {
		e.updateInputModeLT()
	}
	return nil
}

func (e *Engine) CursorUp() *dbus.Error {
	e.Lock()
	defer e.Unlock()
	if e.isEmojiLTOpened && e.emojiLookupTable.CursorUp() {
		e.updateEmojiLookupTable()
	}
	if e.isInputModeLTOpened && e.inputModeLookupTable.CursorUp() {
		e.updateInputModeLT()
	}
	return nil
}

func (e *Engine) CursorDown() *dbus.Error {
	e.Lock()
	defer e.Unlock()
	if e.isEmojiLTOpened && e.emojiLookupTable.CursorDown() {
		e.updateEmojiLookupTable()
	}
	if e.isInputModeLTOpened && e.inputModeLookupTable.CursorDown() {
		e.updateInputModeLT()
	}
	return nil
}

func (e *Engine) CandidateClicked(index uint32, button uint32, state uint32) *dbus.Error {
	e.Lock()
	defer e.Unlock()
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
	before := *e.config

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
			e.setGame(wm != "" && isGameWindow(wm))
		} else {
			e.config.IBflags &= ^config.IBdisableOnGame
			e.setGame(false)
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
		e.persistConfigChangeLocked(&before)
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
	e.bumpConfigGen()
	e.RegisterProperties(e.propList)
	return nil
}
