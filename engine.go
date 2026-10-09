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
	"ktelex/ui"
)

type Engine struct {
	sync.Mutex
	IEngine
	preeditor              bamboo.IEngine
	engineName             string
	config                 *config.Config
	propList               *ibus.PropList
	respectAppMapping      bool   // EN engine ignores per-app InputModeMapping
	englishMode            bool
	macroTable             *MacroTable
	wmClasses              string
	isInputModeLTOpened    bool
	isEmojiLTOpened        bool
	emojiLookupTable       *ibus.LookupTable
	inputModeLookupTable   *ibus.LookupTable
	capabilities           uint32
	keyPressDelay          int
	isFirstTimeSendingBS   bool
	// Uinput BS counting + deferred commit (mirrors skey's approach)
	uinputDeleting_         bool    // true while waiting for BS echoes
	uinputExpectingBs_      int     // BS echoes we're waiting for
	uinputSeenBs_           int     // BS echoes counted so far
	uinputOwedBs_           int     // BS echoes still owed when tx completed early (debt counter)
	uinputOwedExpiresAt_    time.Time // expiry for owed BS echo debt
	uinputPendingCommit_    string  // text to commit after all BS done
	uinputDeferredKeys_     []uint32 // buffered key vals during pending BS
	uinputDeferredCodes_    []uint32 // buffered key codes
	uinputDeferredStates_   []uint32 // buffered key states
	uinputCommitTimer_      *time.Timer // async commit timer (skey-style)
	uinputBsSentAt_         time.Time // when BS was sent via uinput (for EWMA)
	uinputBsRtEwmaUsec_     int64     // EWMA of BS round-trip in µs
	uinputNoEcho_           bool      // true: current app doesn't echo BS through IBus
	uinputLastWm_           string    // wmClass that "owns" the current preedit
	uinputTxID_             uint64    // generation counter for the active transaction
	uinputTxEndAt_          time.Time // when the last transaction ended (stray-BS guard window)
	emoji                  *EmojiEngine
	isSurroundingTextReady bool
	lastKeyWithShift       bool
	lastCommitText         int64
	// restore key strokes by pressing Shift + Space
	shouldRestoreKeyStrokes bool
	// enqueue key strokes to process later
	shouldEnqueuKeyStrokes bool
	isGame                 bool // cached: current focused app is a game
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
	fmt.Printf("\n")
	log.Printf(">>>>ProcessKeyEvent >  %d | state %d keyVal 0x%04x | %c <<<<\n", len(keyPressChan), state, keyVal, rune(keyVal))

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
	if e.inBackspaceWhiteList() {
		return e.bsProcessKeyEvent(keyVal, keyCode, state)
	}
	if e.checkInputMode(config.UinputIM) {
		return e.uinputProcessKeyEvent(keyVal, keyCode, state)
	}
	return e.preeditProcessKeyEvent(keyVal, keyCode, state)
}

func (e *Engine) FocusIn() *dbus.Error {
	// T9: a fresh focus never inherits a half-open tx — end it first so a
	// stale fallback timer cannot commit into the new context.
	e.Lock()
	if e.checkInputMode(config.UinputIM) && (e.uinputDeleting_ || e.uinputPendingCommit_ != "") {
		if e.uinputCommitTimer_ != nil {
			e.uinputCommitTimer_.Stop()
			e.uinputCommitTimer_ = nil
		}
		e.uinputEndTxLocked(e.uinputTxID_, "focusin")
	}
	e.Unlock()
	var latestWm = e.getLatestWmClass()
	log.Printf("FocusIn: %s (IBflags=%d bit21=%d)", latestWm, e.config.IBflags, (e.config.IBflags>>21)&1)
	e.checkWmClass(latestWm)
	if e.config.IBflags&config.IBdisableOnGame != 0 {
		pid := getFocusedPID()
		e.isGame = isGameProcess(pid)
		if e.isGame {
			log.Printf("[gameDetect] input disabled for PID %d (%s)", pid, latestWm)
		} else {
			log.Printf("[gameDetect] PID %d (%s) not a game", pid, latestWm)
		}
	} else {
		e.isGame = false
	}
	e.RegisterProperties(e.propList)
	e.RequireSurroundingText()
	if e.isShortcutKeyEnable(KSEmojiDialog) && emojiTrie != nil && len(emojiTrie.Children) == 0 {
		var err error
		emojiTrie, err = loadEmojiOne(DictEmojiOne)
		if err != nil {
			panic(fmt.Sprintf("failed to load emojiTrie from %s: %s", DictEmojiOne, err))
		}
	}
	if e.config.IBflags&config.IBspellCheckWithDicts != 0 && len(dictionary) == 0 {
		dictionary, _ = loadDictionary(DictVietnameseCm)
	}
	fmt.Printf("WM_CLASS=(%s)\n", e.getWmClass())
	return nil
}

func (e *Engine) FocusOut() *dbus.Error {
	log.Print("FocusOut.")
	if e.checkInputMode(config.UinputIM) {
		// In UinputIM, browsers (Thorium, Chromium, Electron) frequently flap FocusOut/FocusIn
		// on Backspace injection and DOM edits.
		// We do NOT reset preeditor or wipe uinputLastWm_ here because doing so splits syllables
		// mid-word (e.g. "tiê" -> reset -> "ngs" -> "tiêngs").
		// If the user actually switches apps, the switch is caught by checkWmClass/uinputProcessKeyEvent
		// or FocusIn on the new window.
		// If a transaction is actively deleting or pending commit, we let the timer/echo complete normally.
		return nil
	}
	e.resetPreedit()
	return nil
}

func (e *Engine) Reset() *dbus.Error {
	fmt.Print("Reset.\n")
	if e.checkInputMode(config.PreeditIM) {
		e.preeditor.Reset()
	}
	return nil
}

func (e *Engine) Enable() *dbus.Error {
	fmt.Print("Enable.\n")
	e.preeditor.Reset()
	e.RequireSurroundingText()
	return nil
}

func (e *Engine) Disable() *dbus.Error {
	fmt.Print("Disable.\n")
	e.Lock()
	if e.uinputCommitTimer_ != nil {
		e.uinputCommitTimer_.Stop()
		e.uinputCommitTimer_ = nil
	}
	e.uinputExpectingBs_ = 0
	e.uinputSeenBs_ = 0
	e.uinputDeleting_ = false
	e.uinputPendingCommit_ = ""
	e.uinputDeferredKeys_ = nil
	e.uinputDeferredCodes_ = nil
	e.uinputDeferredStates_ = nil
	e.uinputNoEcho_ = false
	e.Unlock()
	e.preeditor.Reset()
	return nil
}

// @method(in_signature="vuu")
func (e *Engine) SetSurroundingText(text dbus.Variant, cursorPos uint32, anchorPos uint32) *dbus.Error {
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
		fmt.Println("Surrounding Text: ", string(cs))
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

func (e *Engine) SetContentType(purpose uint32, hints uint32) *dbus.Error {
	return nil
}

// @method(in_signature="su")
func (e *Engine) PropertyActivate(propName string, propState uint32) *dbus.Error {
	if propName == PropKeyAbout {
		exec.Command("xdg-open", HomePage).Start()
		return nil
	}
	if propName == PropKeyConfiguration {
		ui.OpenGUI(e.engineName, Version, BuildId)
		e.config = config.LoadConfig(e.engineName)
		e.FocusOut()
		e.FocusIn()
	}
	if propName == PropKeyInputModeLookupTableShortcut {
		ui.OpenGUI(e.engineName, Version, BuildId)
		e.config = config.LoadConfig(e.engineName)
		e.FocusOut()
		e.FocusIn()
	}
	if propName == PropKeyMacroTable {
		ui.OpenGUI(e.engineName, Version, BuildId)
		e.config = config.LoadConfig(e.engineName)
		e.FocusOut()
		e.FocusIn()
	}

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
			dictionary, _ = loadDictionary(DictVietnameseCm)
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
			pid := getFocusedPID()
			e.isGame = isGameProcess(pid)
			if e.isGame {
				log.Printf("[gameDetect] input disabled for current PID %d (%s)", pid, e.getWmClass())
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

	var inputMethod = bamboo.ParseInputMethod(e.config.InputMethodDefinitions, e.config.InputMethod)
	if inputMethod.Name == "" {
		inputMethod = bamboo.ParseInputMethod(e.config.InputMethodDefinitions, bamboo.DefaultInputMethodName)
		e.config.InputMethod = bamboo.DefaultInputMethodName
	}
	e.preeditor = bamboo.NewEngine(inputMethod, e.config.Flags)
	e.RegisterProperties(e.propList)
	return nil
}
