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
	"ktelex/config"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BambooEngine/bamboo-core"
	ibus "github.com/BambooEngine/goibus"
	"github.com/godbus/dbus/v5"
)

func (e *Engine) preeditProcessKeyEvent(keyVal uint32, keyCode uint32, state uint32) (bool, *dbus.Error) {
	var rawKeyLen = e.getRawKeyLen()
	var keyRune = rune(keyVal)
	var oldText = e.getPreeditString()
	defer e.updateLastKeyWithShift(keyVal, state)

	if !e.shouldRestoreKeyStrokes {
		if !e.preeditor.CanProcessKey(keyRune) && rawKeyLen == 0 && e.config.IBflags&config.IBmacroEnabled == 0 {
			// don't process special characters if rawKeyLen == 0,
			// workaround for Chrome's address bar and Google SpreadSheets
			return false, nil
		}
	}

	if keyVal == IBusBackSpace {
		if e.runeCount() == 1 {
			e.commitPreeditAndReset("")
			return true, nil
		}
		if rawKeyLen > 0 {
			e.preeditor.RemoveLastChar(true)
			e.updatePreedit(e.getPreeditString())
			return true, nil
		} else {
			return false, nil
		}
	}
	if keyVal == IBusTab {
		if ok, macText := e.getMacroText(); ok {
			e.commitPreeditAndReset(macText)
		} else {
			e.commitPreeditAndReset(e.getComposedString(oldText))
			return false, nil
		}
		return true, nil
	}

	newText, isWordBreakRune := e.getCommitText(keyVal, keyCode, state)
	isPrintableKey := e.isPrintableKey(state, keyVal)
	if isWordBreakRune {
		e.commitPreeditAndResetForWBS(newText, isPrintableKey)
		return isPrintableKey, nil
	}
	if e.checkInputMode(config.UinputIM) {
		return isPrintableKey, nil
	}
	e.updatePreedit(newText)
	return isPrintableKey, nil
}

// uinputPreeditString returns the raw Vietnamese-mode preedit string,
// bypassing the shouldFallbackToEnglish validity check. In UinputIM,
// we always need the real Vietnamese transformation state — the
// shouldFallbackToEnglish logic is designed for PreeditIM mode where
// invalid compositions are auto-reverted to English keystrokes.
func (e *Engine) uinputPreeditString() string {
	return e.getProcessedString(bamboo.VietnameseMode)
}

// uinputReplayBuffered replays all keys that were buffered during uinput
// BS flight, via ForwardKeyEvent (IBus D-Bus).
func (e *Engine) uinputReplayBuffered() {
	keys := e.uinputDeferredKeys_
	codes := e.uinputDeferredCodes_
	states := e.uinputDeferredStates_
	e.uinputDeferredKeys_ = nil
	e.uinputDeferredCodes_ = nil
	e.uinputDeferredStates_ = nil
	for i := range keys {
		log.Printf("[uinputIM] replay buffered key 0x%04x", keys[i])
		e.ForwardKeyEvent(keys[i], codes[i], states[i])
		e.ForwardKeyEvent(keys[i], codes[i], states[i]|IBusReleaseMask)
	}
}

// EWMA constants for adaptive commit delay (mirrors skey's kBsRtEwmaAlpha etc.)
const (
	kUinputEwmaAlpha      = 0.5
	kUinputMinUsec        = 10000 // 10ms minimum
	kUinputInitUsec       = 15000 // 15ms initial EWMA seed
	kUinputMultiplier     = 1.0
	kUinputAddrBarUsec    = 25000 // 25ms for address bar
	kUinputStaleTimeout   = 300 * time.Millisecond // watchdog: reset stuck state
	kUinputFixedCommitMs  = 20                      // fixed delay for uinput BS → commit
)

// computeUinputCommitDelay calculates adaptive delay using EWMA of measured
// round-trip times (bsSentAt → last BS echo received).
func computeUinputCommitDelay(rt time.Duration, ewma *int64) time.Duration {
	rtUsec := rt.Microseconds()
	if *ewma <= 0 || *ewma == kUinputInitUsec {
		*ewma = rtUsec
	} else {
		*ewma = int64(kUinputEwmaAlpha*float64(rtUsec) + (1.0-kUinputEwmaAlpha)*float64(*ewma))
	}
	delayUsec := *ewma
	if delayUsec < kUinputMinUsec {
		delayUsec = kUinputMinUsec
	}
	return time.Duration(delayUsec) * time.Microsecond
}

// uinputProcessKeyEvent handles UinputIM on Wayland.
//
// Modeled after skey's uinput mechanism (skey/src/engine.cpp).
//
// Key design:
//   - Simple append (old+char==new): forward raw (return false), zero latency.
//   - Transformation: consume key (return true), send uinput BS via evdev.
//     BS echoes are FORWARDED (return false) because on Wayland the evdev
//     BS events are routed through IBus → the BS echo IS the evdev BS.
//     After all BS echoes counted, schedule async timer for commitText
//     (skey-style) so the app has time to process evdev BS events before
//     the replacement text arrives via D-Bus.
//   - Non-BS keys during delete are BUFFERED and replayed after commit.
//   - Ctrl/Alt+letter: forward raw so find/copy/paste/nav work.
//   - Backspace from user: update engine state, forward raw BS to app.
//   - Commit delay uses EWMA of measured round-trip times (adaptive).
func (e *Engine) uinputProcessKeyEvent(keyVal uint32, keyCode uint32, state uint32) (bool, *dbus.Error) {
	var keyRune = rune(keyVal)

	// Log ghost BackSpace events while engine is idle
	if keyVal == IBusBackSpace && !e.uinputDeleting_ && e.uinputExpectingBs_ == 0 {
		log.Printf("[uinputIM] GHOST BS while idle! keyCode=0x%04x state=0x%04x", keyCode, state)
	}

	// Debug: log ALL printable key events along with preedit state
	wm := e.getWmClass()
	if keyRune >= 'a' && keyRune <= 'z' && !e.uinputDeleting_ {
		log.Printf("[uinputIM] key=%c preedit=%q wm=%s", keyRune, e.uinputPreeditString(), wm)
	}

	// If FocusOut reset preedit state, track which app "owns" the preedit
	if e.uinputPreeditString() != "" && wm != e.uinputLastWm_ {
		log.Printf("[uinputIM] app change: old=%s new=%s, reset preedit", e.uinputLastWm_, wm)
		e.preeditor.Reset()
	}
	e.uinputLastWm_ = wm

	defer e.updateLastKeyWithShift(keyVal, state)
	defer func() {
		if keyRune >= 'a' && keyRune <= 'z' {
			log.Printf("[uinputIM] FINAL: after %c -> %q (rawKeyLen=%d)",
				keyRune, e.uinputPreeditString(), e.getRawKeyLen())
		}
	}()

	// ── Handle uinput delete state ──
	// While uinputDeleting_ is true, ALL keys arriving via IBus are
	// in-flight while evdev BS events travel through kernel compositor.
	// BS echoes are FORWARDED so the app receives its BackSpace events.
	// Non-BS keys are BUFFERED and replayed after commit.
	if e.uinputDeleting_ {
		// Watchdog: if BS echoes stall (lost/merged by compositor, or
		// app doesn't route evdev through IBus like Chromium Wayland),
		// force-reset after timeout to prevent permanently stuck state.
		if e.uinputExpectingBs_ > 0 && time.Since(e.uinputBsSentAt_) > kUinputStaleTimeout {
			if !e.uinputNoEcho_ {
				e.uinputNoEcho_ = true
				e.uinputBsRtEwmaUsec_ = kUinputInitUsec
				log.Printf("[uinputIM] WATCHDOG: app '%s' doesn't echo BS (seen=%d expect=%d)",
					e.getWmClass(), e.uinputSeenBs_, e.uinputExpectingBs_)
			}
			e.uinputExpectingBs_ = 0
			e.uinputSeenBs_ = 0
			e.uinputDeleting_ = false
			forceText := e.uinputPendingCommit_
			e.uinputPendingCommit_ = ""
			if forceText != "" {
				e.commitText(forceText)
			}
			e.uinputDeferredKeys_ = nil
			e.uinputDeferredCodes_ = nil
			e.uinputDeferredStates_ = nil
		} else if e.uinputNoEcho_ {
			// No-echo app — flush commit, forward key.
			if e.uinputPendingCommit_ != "" {
				e.commitText(e.uinputPendingCommit_)
				e.uinputPendingCommit_ = ""
			}
			e.uinputDeleting_ = false
			e.uinputExpectingBs_ = 0
			e.uinputSeenBs_ = 0
			e.uinputDeferredKeys_ = nil
			e.uinputDeferredCodes_ = nil
			e.uinputDeferredStates_ = nil
		} else if keyVal == IBusBackSpace {
			e.uinputSeenBs_++
			log.Printf("[uinputIM] BS echo %d/%d (forward)", e.uinputSeenBs_, e.uinputExpectingBs_)
			if e.uinputSeenBs_ >= e.uinputExpectingBs_ {
				if e.uinputNoEcho_ {
					e.uinputNoEcho_ = false
					log.Printf("[uinputIM] BS echoes arrived, noEcho=false (recovered)")
				}
				rt := time.Since(e.uinputBsSentAt_)
				delay := computeUinputCommitDelay(rt, &e.uinputBsRtEwmaUsec_)
				e.uinputExpectingBs_ = 0
				e.uinputSeenBs_ = 0
				cText := e.uinputPendingCommit_
				e.uinputPendingCommit_ = ""
				bufKeys := e.uinputDeferredKeys_
				bufCodes := e.uinputDeferredCodes_
				bufStates := e.uinputDeferredStates_
				e.uinputDeferredKeys_ = nil
				e.uinputDeferredCodes_ = nil
				e.uinputDeferredStates_ = nil
				if e.uinputCommitTimer_ != nil {
					e.uinputCommitTimer_.Stop()
				}
				log.Printf("[uinputIM] all BS done, RT=%v, commit %q in %v", rt, cText, delay)
				e.uinputCommitTimer_ = time.AfterFunc(delay, func() {
					e.Lock()
					if !e.uinputDeleting_ {
						e.Unlock()
						return
					}
					e.uinputDeleting_ = false
					e.Unlock()
					if cText != "" {
						log.Printf("[uinputIM] timer commit: %q", cText)
						e.commitText(cText)
					}
					for i := range bufKeys {
						log.Printf("[uinputIM] replay buffered key 0x%04x", bufKeys[i])
						e.ForwardKeyEvent(bufKeys[i], bufCodes[i], bufStates[i])
						e.ForwardKeyEvent(bufKeys[i], bufCodes[i], bufStates[i]|IBusReleaseMask)
					}
				})
			}
			return false, nil
		} else {
			if keyVal >= 0xffe1 && keyVal <= 0xfff0 {
				return false, nil
			}
			if len(e.uinputDeferredKeys_) < 32 {
				log.Printf("[uinputIM] buffer key 0x%04x during uinput delete", keyVal)
				e.uinputDeferredKeys_ = append(e.uinputDeferredKeys_, keyVal)
				e.uinputDeferredCodes_ = append(e.uinputDeferredCodes_, keyCode)
				e.uinputDeferredStates_ = append(e.uinputDeferredStates_, state)
			}
			return true, nil
		}
	}

	// ── Forward Ctrl/Alt+letter raw to the app ──
	if state&(IBusControlMask|IBusMod1Mask) != 0 && keyRune >= 'a' && keyRune <= 'z' {
		log.Printf("[uinputIM] Ctrl/Alt+%c: forward to app", keyRune)
		return false, nil
	}

	// Skip non-composing keys when engine is idle
	if !e.shouldRestoreKeyStrokes {
		if !e.preeditor.CanProcessKey(keyRune) && e.getRawKeyLen() == 0 && e.config.IBflags&config.IBmacroEnabled == 0 {
			return false, nil
		}
	}

	// ── Handle user backspace ──
	if keyVal == IBusBackSpace {
		if e.getRawKeyLen() > 0 {
			e.preeditor.RemoveLastChar(true)
			log.Printf("[uinputIM] BS: removeLastChar -> %q (forward to app)", e.uinputPreeditString())
		}
		return false, nil
	}

	// ── Tab ──
	if keyVal == IBusTab {
		if ok, macText := e.getMacroText(); ok {
			rawLen := e.getRawKeyLen()
			if rawLen > 0 {
				log.Printf("[uinputIM] macro: delete %d then commit %q", rawLen, macText)
				e.uinputBsSentAt_ = time.Now()
				e.uinputExpectingBs_ = rawLen
				e.uinputSeenBs_ = 0
				e.uinputPendingCommit_ = macText
				e.uinputDeleting_ = true
				uinputBackspace(rawLen)
			} else {
				e.commitText(macText)
			}
			return true, nil
		}
		return false, nil
	}

	// ── Word break (space/punctuation) ──
	if bamboo.IsWordBreakSymbol(keyRune) {
		if e.getRawKeyLen() > 0 {
			log.Printf("[uinputIM] word break %q: reset preeditor, forward", string(keyRune))
			e.resetPreedit()
		}
		return false, nil
	}

	// ── Non-printable / function keys ──
	if !e.isPrintableKey(state, keyVal) {
		if e.getRawKeyLen() > 0 {
			log.Printf("[uinputIM] funct key 0x%04x: reset preedit=%q, forward", keyVal, e.uinputPreeditString())
			e.resetPreedit()
		}
		return false, nil
	}

	// ── Process key in VietnameseMode ──
	oldText := e.uinputPreeditString()
	keyS := string(keyRune)
	log.Printf("[uinputIM] PRE: oldText=%q keyS=%q rawKeyLen=%d mode=%v",
		oldText, keyS, e.getRawKeyLen(), e.getInputMethodMode())

	e.preeditor.ProcessKey(keyRune, bamboo.VietnameseMode)
	afterText := e.uinputPreeditString()

	log.Printf("[uinputIM] POST: afterText=%q", afterText)

	// Simple append: forward raw key
	if oldText+keyS == afterText {
		log.Printf("[uinputIM] simple append: forward %q", keyS)
		return false, nil
	}

	// Transformation: uinput BS + timer-based commit
	pfxRunes := runePrefixLen(oldText, afterText)
	delPart := string([]rune(oldText)[pfxRunes:])
	addPart := string([]rune(afterText)[pfxRunes:])
	deleteLen := utf8.RuneCountInString(delPart)

	log.Printf("[uinputIM] transform: %q + %q -> %q (pfx=%d del=%d add=%q)",
		oldText, keyS, afterText, pfxRunes, deleteLen, addPart)

	if deleteLen > 0 {
		if e.uinputNoEcho_ {
			log.Printf("[uinputIM] no-echo: FwdKey BS=%d + type %q", deleteLen, addPart)
			for i := 0; i < deleteLen; i++ {
				e.ForwardKeyEvent(IBusBackSpace, 0, 0)
				e.ForwardKeyEvent(IBusBackSpace, 0, IBusReleaseMask)
			}
			for _, r := range addPart {
				keyVal := uint32(r)
				if keyVal > 0xFF {
					keyVal = 0x01000000 + keyVal
				}
				e.ForwardKeyEvent(keyVal, 0, 0)
				e.ForwardKeyEvent(keyVal, 0, IBusReleaseMask)
			}
		} else {
			log.Printf("[uinputIM] uinput BS=%d, pending commit=%q", deleteLen, addPart)
			e.uinputBsSentAt_ = time.Now()
			e.uinputExpectingBs_ = deleteLen
			e.uinputSeenBs_ = 0
			e.uinputPendingCommit_ = addPart
			e.uinputDeleting_ = true
			uinputBackspace(deleteLen)
		}
	} else if addPart != "" {
		e.commitText(addPart)
	} else {
		log.Printf("[uinputIM] WARNING: transform but deleteLen=0 addPart=empty")
	}

	return true, nil
}

func (e *Engine) debugPreeditReset(from string) {
	if strings.Contains(e.getWmClass(), "floorp") {
		log.Printf("[floorp] PREEDIT RESET by %s", from)
	}
}

// runePrefixLen returns the number of leading runes common to both strings.
// Unlike the byte-based prefix, this is SAFE for multi-byte UTF-8 strings
// like Vietnamese accented characters (e.g. "á"→"à": prefix=0 runes, not 1 byte).
func runePrefixLen(a, b string) int {
	aRunes := []rune(a)
	bRunes := []rune(b)
	minLen := len(aRunes)
	if len(bRunes) < minLen {
		minLen = len(bRunes)
	}
	for i := 0; i < minLen; i++ {
		if aRunes[i] != bRunes[i] {
			return i
		}
	}
	return minLen
}

// uinputBsFixedDelay sends BS via uinput and enters delete state without
// echo counting.  The NEXT non-BS key flushes the commit immediately —
// no buffering, no delay.  A watchdog timer catches the case where the
// user doesn't type a follow-up key (commits after 300ms).
func (e *Engine) uinputBsFixedDelay(bsCount int, commitText string) {
	log.Printf("[uinputIM] fixed-delay: BS=%d commit=%q", bsCount, commitText)
	e.uinputBsSentAt_ = time.Now()
	e.uinputExpectingBs_ = bsCount
	e.uinputSeenBs_ = 0
	e.uinputPendingCommit_ = commitText
	e.uinputDeleting_ = true
	uinputBackspace(bsCount)
	// Passthrough to protect from typeCodepoint loopback
	addLen := utf8.RuneCountInString(commitText)
	ms := time.Duration(bsCount + 3 + addLen*12 + 10) * time.Millisecond
	e.uinputPassthroughUntil_ = time.Now().Add(ms)
}

func (e *Engine) expandMacro(str string) string {
	var macroText = e.macroTable.GetText(str)
	if e.config.IBflags&config.IBautoCapitalizeMacro != 0 {
		switch determineMacroCase(str) {
		case VnCaseAllSmall:
			return strings.ToLower(macroText)
		case VnCaseAllCapital:
			return strings.ToUpper(macroText)
		}
	}
	return macroText
}

func (e *Engine) updatePreedit(processedStr string) {
	var encodedStr = e.encodeText(processedStr)
	var preeditLen = uint32(len([]rune(encodedStr)))
	if preeditLen == 0 {
		e.HidePreeditText()
		e.HideAuxiliaryText()
		e.CommitText(ibus.NewText(""))
		return
	}
	var ibusText = ibus.NewText(encodedStr)
	if inStringList(enabledAuxiliaryTextList, e.getWmClass()) {
		e.UpdateAuxiliaryText(ibusText, true)
		return
	}

	if !e.checkInputMode(config.UinputIM) && !e.checkInputMode(config.UsIM) {
		ibusText.AppendAttr(ibus.IBUS_ATTR_TYPE_UNDERLINE, ibus.IBUS_ATTR_UNDERLINE_SINGLE, 0, preeditLen)
	}
	e.UpdatePreeditTextWithMode(ibusText, preeditLen, true, ibus.IBUS_ENGINE_PREEDIT_COMMIT)
}

func (e *Engine) getInputMethodMode() bamboo.Mode {
	if e.config.DefaultInputMode == config.UsIM {
		return bamboo.EnglishMode
	}
	if e.shouldFallbackToEnglish(false) {
		return bamboo.EnglishMode
	}
	return bamboo.VietnameseMode
}

func (e *Engine) shouldFallbackToEnglish(checkVnRune bool) bool {
	if e.config.IBflags&config.IBautoNonVnRestore == 0 {
		return false
	}
	var vnSeq = e.getProcessedString(bamboo.VietnameseMode | bamboo.LowerCase)
	var vnRunes = []rune(vnSeq)
	if len(vnRunes) == 0 {
		return false
	}
	if ok, _ := e.getMacroText(); ok {
		return false
	}
	// we want to allow dd even in non-vn sequence, because dd is used a lot in abbreviation
	if e.config.IBflags&config.IBddFreeStyle != 0 && !bamboo.HasAnyVietnameseVower(vnSeq) &&
		(vnRunes[len(vnRunes)-1] == 'd' || strings.ContainsRune(vnSeq, 'đ')) {
		return false
	}
	if checkVnRune && !bamboo.HasAnyVietnameseRune(vnSeq) {
		return false
	}
	return !e.preeditor.IsValid(false)
}

func (e *Engine) mustFallbackToEnglish() bool {
	if e.config.IBflags&config.IBautoNonVnRestore == 0 {
		return false
	}
	var vnSeq = e.getProcessedString(bamboo.VietnameseMode | bamboo.LowerCase)
	var vnRunes = []rune(vnSeq)
	if len(vnRunes) == 0 {
		return false
	}
	// we want to allow dd even in non-vn sequence, because dd is used a lot in abbreviation
	if e.config.IBflags&config.IBddFreeStyle != 0 && strings.ContainsRune(vnSeq, 'đ') {
		return false
	}
	if e.config.IBflags&config.IBspellCheckWithDicts != 0 {
		return !dictionary[vnSeq]
	}
	return !e.preeditor.IsValid(true)
}

func (e *Engine) getComposedString(oldText string) string {
	if bamboo.HasAnyVietnameseRune(oldText) && e.mustFallbackToEnglish() {
		return e.getProcessedString(bamboo.EnglishMode)
	}
	return oldText
}

func (e *Engine) encodeText(text string) string {
	return bamboo.Encode(e.config.OutputCharset, text)
}

func (e *Engine) getProcessedString(mode bamboo.Mode) string {
	return e.preeditor.GetProcessedString(mode)
}

func (e *Engine) getPreeditString() string {
	if e.config.IBflags&config.IBmacroEnabled != 0 {
		return e.getProcessedString(bamboo.PunctuationMode)
	}
	if e.shouldFallbackToEnglish(true) {
		return e.getProcessedString(bamboo.EnglishMode)
	}
	return e.getProcessedString(bamboo.VietnameseMode)
}

func (e *Engine) resetPreedit() {
	e.HidePreeditText()
	e.HideAuxiliaryText()
	e.preeditor.Reset()
}

func (e *Engine) commitPreeditAndResetForWBS(s string, isWBS bool) {
	if isWBS {
		// Fix missing the first word while typing in FB Messager as FB prefers
		// committing text before hiding preedit
		e.commitText(s)
		e.HidePreeditText()
	} else {
		e.HidePreeditText()
		e.commitText(s)
	}
	e.HideAuxiliaryText()
	e.HideLookupTable()
	e.preeditor.Reset()
}

func (e *Engine) commitPreeditAndReset(s string) {
	e.HidePreeditText()
	e.HideAuxiliaryText()
	e.HideLookupTable()
	e.commitText(s)
	e.preeditor.Reset()
}

func (e *Engine) commitText(str string) {
	if str == "" {
		return
	}
	log.Printf("Commit Text [%s]\n", str)
	var now = time.Now()
	e.lastCommitText = now.UnixNano()
	e.CommitText(ibus.NewText(e.encodeText(str)))
}

func (e *Engine) getVnSeq() string {
	return e.preeditor.GetProcessedString(bamboo.VietnameseMode)
}

func (e *Engine) hasMacroKey(key string) bool {
	return e.macroTable.GetText(key) != ""
}
