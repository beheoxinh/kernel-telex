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
	e.uinputDeferredKeys_ = nil // T8: snapshot+clear, replay below
	e.uinputDeferredCodes_ = nil
	e.uinputDeferredStates_ = nil
	for i := range keys {
		log.Printf("[uinputIM] replay buffered key 0x%04x", keys[i])
		e.ForwardKeyEvent(keys[i], codes[i], states[i])
		e.ForwardKeyEvent(keys[i], codes[i], states[i]|IBusReleaseMask)
	}
}

// updateUinputEwma folds a measured BS-echo round trip into the EWMA.
// Latency signal only: commit timing always goes through uinputTxTimeout.
func (e *Engine) updateUinputEwma(rt time.Duration) {
	rtUsec := rt.Microseconds()
	if e.uinputBsRtEwmaUsec_ <= 0 || e.uinputBsRtEwmaUsec_ == kUinputInitUsec {
		e.uinputBsRtEwmaUsec_ = rtUsec
	} else {
		e.uinputBsRtEwmaUsec_ = int64(kUinputEwmaAlpha*float64(rtUsec) + (1.0-kUinputEwmaAlpha)*float64(e.uinputBsRtEwmaUsec_))
	}
}

// EWMA constants for adaptive commit delay (mirrors skey's kBsRtEwmaAlpha etc.)
const (
	kUinputEwmaAlpha      = 0.5
	kUinputMinUsec        = 10000 // 10ms minimum commit delay
	kUinputInitUsec       = 15000 // 15ms initial EWMA seed
	kUinputStaleTimeout   = 300 * time.Millisecond // watchdog: reset stuck state
	kUinputTxMinUsec      = 20000                   // 20ms base lower bound for fallback timer
	kUinputTxMaxUsec      = 150000                  // 150ms upper bound for fallback timer
	kUinputBsPeriodUsec   = 4000                    // 4ms per BS hardware inject duration (3ms press + 1ms release)
	kUinputTxSafetyUsec   = 25000                   // 25ms safety margin for fallback timer
	kUinputStrayBsGuard   = 80 * time.Millisecond   // secondary window for stray duplicate echoes
	kUinputOwedExpiry     = 300 * time.Millisecond  // watchdog expiration for echo debt
)

// computeCommitDelay calculates adaptive delay (in µs) between last BS echo
// and CommitText so the client application has time to process the Backspace
// before replacement text arrives.
func (e *Engine) computeCommitDelay(rt time.Duration) time.Duration {
	e.updateUinputEwma(rt)
	delayUsec := e.uinputBsRtEwmaUsec_
	if delayUsec < kUinputMinUsec {
		delayUsec = kUinputMinUsec
	}
	if delayUsec > 30000 {
		delayUsec = 30000 // cap commit delay at 30ms
	}
	return time.Duration(delayUsec) * time.Microsecond
}

// uinputTxTimeout calculates dynamic fallback timeout taking into account
// the physical hardware injection time (deleteLen * 4ms), measured round-trip EWMA,
// and a safety margin.
func uinputTxTimeout(ewmaUsec int64, deleteLen int) time.Duration {
	if deleteLen < 1 {
		deleteLen = 1
	}
	injectUsec := int64(deleteLen) * kUinputBsPeriodUsec
	calcUsec := injectUsec + ewmaUsec + kUinputTxSafetyUsec
	minUsec := injectUsec + kUinputTxMinUsec
	if calcUsec < minUsec {
		calcUsec = minUsec
	}
	if calcUsec > kUinputTxMaxUsec {
		calcUsec = kUinputTxMaxUsec
	}
	return time.Duration(calcUsec) * time.Microsecond
}

// uinputEndTxLocked finishes the active transaction: stops the fallback
// timer, clears delete state, records the stray-BS guard timestamp,
// tracks echo debt if tx ended with in-flight BS echoes, commits the
// replacement text via IBus CommitText, and replays every deferred key.
// Call with e.Mutex held; releases it around I/O.
func (e *Engine) uinputEndTxLocked(txID uint64, reason string) {
	if e.uinputTxID_ != txID {
		return
	}
	if e.uinputCommitTimer_ != nil {
		e.uinputCommitTimer_.Stop()
		e.uinputCommitTimer_ = nil
	}

	// Echo debt tracking: if the transaction finished with seen < expect
	// (e.g. fallback timer fired or wm-change), record remaining echoes as debt
	// so late arrivals are swallowed instead of treated as user backspaces.
	if e.uinputExpectingBs_ > e.uinputSeenBs_ {
		debt := e.uinputExpectingBs_ - e.uinputSeenBs_
		e.uinputOwedBs_ += debt
		e.uinputOwedExpiresAt_ = time.Now().Add(kUinputOwedExpiry)
		log.Printf("[uinputIM] tx end (%s): recorded echo debt=%d (total owed=%d)", reason, debt, e.uinputOwedBs_)
	}

	cText := e.uinputPendingCommit_
	e.uinputPendingCommit_ = ""
	e.uinputDeleting_ = false
	e.uinputExpectingBs_ = 0
	e.uinputSeenBs_ = 0
	keys := e.uinputDeferredKeys_
	codes := e.uinputDeferredCodes_
	states := e.uinputDeferredStates_
	e.uinputDeferredKeys_ = nil
	e.uinputDeferredCodes_ = nil
	e.uinputDeferredStates_ = nil
	e.uinputTxEndAt_ = time.Now()
	e.uinputNoEcho_ = false
	log.Printf("[uinputIM] tx end (%s): commit %q, replay %d keys", reason, cText, len(keys))
	if cText != "" {
		e.commitText(cText)
	}
	for i := range keys {
		log.Printf("[uinputIM] replay buffered key 0x%04x", keys[i])
		e.ForwardKeyEvent(keys[i], codes[i], states[i])
		e.ForwardKeyEvent(keys[i], codes[i], states[i]|IBusReleaseMask)
	}
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
	// T5 race fix: the tx timer goroutine touches uinputDeleting_/SeenBs/
	// ExpectingBs under e.Mutex. The keyevent path must hold the same mutex
	// for every tx-state access, otherwise `go test -race` fires. All
	// callees (preeditor, commitText, uinputBackspace, getWmClass, …) are
	// lock-free (audited), and uinputEndTxLocked releases the mutex around
	// its D-Bus/evdev I/O, so holding it here cannot deadlock.
	e.Lock()
	defer e.Unlock()
	var keyRune = rune(keyVal)

	// Echo guard (T6) + Echo Debt (T10):
	// 1. If we have recorded echo debt from a premature tx end, swallow
	//    the late arriving BS echoes until debt is repaid.
	// 2. If within stray guard window after normal tx end, swallow duplicate echoes.
	if keyVal == IBusBackSpace && !e.uinputDeleting_ {
		if e.uinputOwedBs_ > 0 {
			if time.Now().Before(e.uinputOwedExpiresAt_) {
				e.uinputOwedBs_--
				log.Printf("[uinputIM] swallow owed BS echo (remaining debt=%d)", e.uinputOwedBs_)
				return true, nil
			}
			log.Printf("[uinputIM] owed BS debt expired (clearing %d)", e.uinputOwedBs_)
			e.uinputOwedBs_ = 0
		}
		if !e.uinputTxEndAt_.IsZero() && time.Since(e.uinputTxEndAt_) < kUinputStrayBsGuard {
			log.Printf("[uinputIM] stray BS echo after tx end: swallow (guard %v)", kUinputStrayBsGuard)
			return true, nil
		}
		if e.uinputExpectingBs_ == 0 {
			log.Printf("[uinputIM] GHOST BS while idle! keyCode=0x%04x state=0x%04x", keyCode, state)
		}
	}

	// Debug: log ALL printable key events along with preedit state
	wm := e.getWmClass()
	if keyRune >= 'a' && keyRune <= 'z' && !e.uinputDeleting_ {
		log.Printf("[uinputIM] key=%c preedit=%q wm=%s", keyRune, e.uinputPreeditString(), wm)
	}

	// If FocusOut reset preedit state, track which app "owns" the preedit.
	// T9: a wm change also ends any half-open tx through the single tx-end
	// path (commit + replay), so no keystroke is lost across app switches.
	if e.uinputPreeditString() != "" && e.uinputLastWm_ != "" && wm != "" && !isSameWmClass(wm, e.uinputLastWm_) {
		log.Printf("[uinputIM] app change: old=%s new=%s, reset preedit", e.uinputLastWm_, wm)
		e.preeditor.Reset()
	}
	if e.uinputLastWm_ != "" && wm != "" && !isSameWmClass(wm, e.uinputLastWm_) && (e.uinputDeleting_ || e.uinputPendingCommit_ != "") {
		log.Printf("[uinputIM] wm change with open tx: end tx before switch")
		if e.uinputCommitTimer_ != nil {
			e.uinputCommitTimer_.Stop()
			e.uinputCommitTimer_ = nil
		}
		e.uinputEndTxLocked(e.uinputTxID_, "wm-change")
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
	// BS echoes are SWALLOWED (return true) to prevent double-deletion
	// on synchronous toolkits like JetBrains JBR/AWT and Electron.
	// Non-BS keys are BUFFERED and replayed after commit.
	if e.uinputDeleting_ {
		// T9: the independent tx timer owns the commit; a next key only
		// refreshes the watchdog reference, never force-commits here.
		if e.uinputExpectingBs_ > 0 && time.Since(e.uinputBsSentAt_) > kUinputStaleTimeout {
			log.Printf("[uinputIM] WATCHDOG: no echo progress (seen=%d expect=%d), tx timer owns commit",
				e.uinputSeenBs_, e.uinputExpectingBs_)
		}
		if keyVal == IBusBackSpace {
			e.uinputSeenBs_++
			log.Printf("[uinputIM] BS echo %d/%d (swallowed)", e.uinputSeenBs_, e.uinputExpectingBs_)
			if e.uinputSeenBs_ >= e.uinputExpectingBs_ {
				rt := time.Since(e.uinputBsSentAt_)
				delay := e.computeCommitDelay(rt)
				txID := e.uinputTxID_
				if e.uinputCommitTimer_ != nil {
					e.uinputCommitTimer_.Stop()
				}
				log.Printf("[uinputIM] all BS done, RT=%v, commit in %v", rt, delay)
				e.uinputCommitTimer_ = time.AfterFunc(delay, func() {
					e.Lock()
					defer e.Unlock()
					if e.uinputTxID_ != txID || !e.uinputDeleting_ {
						return
					}
					e.uinputEndTxLocked(txID, "all-echo")
				})
			}
			return false, nil
		}
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

	// ── Handle user backspace & Anti-runaway watchdog ──
	// (T6: stray post-commit echoes are already swallowed above; a BS that
	// reaches here outside the guard window is a genuine user keystroke.)
	if keyVal == IBusBackSpace {
		now := time.Now()
		interval := now.Sub(e.uinputLastIdleBsAt_)
		e.uinputLastIdleBsAt_ = now

		// Anti-runaway watchdog:
		// Rapid backspaces (< 45ms, e.g. 30ms compositor autorepeat) or sustained
		// repeat storms that experience brief JVM GC pauses / stutter (cooldown < 120ms)
		// trigger repeat storm suppression.
		if interval < 45*time.Millisecond || (e.uinputIdleBsBurstCount_ >= 3 && interval < 120*time.Millisecond) {
			e.uinputIdleBsBurstCount_++
			if e.uinputIdleBsBurstCount_ >= 3 {
				// Force release any stuck backspace/modifier key on uinput device
				uinputReleaseAll()
				log.Printf("[uinputIM] RUNAWAY BS STORM DETECTED (burst=%d, intv=%v)! Suppressed & swallowed.",
					e.uinputIdleBsBurstCount_, interval)
				// Swallow the event: do NOT delete from preeditor and do NOT forward to app
				return true, nil
			}
		} else {
			e.uinputIdleBsBurstCount_ = 1
		}

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
				e.uinputTxID_++
				txID := e.uinputTxID_
				if e.uinputCommitTimer_ != nil {
					e.uinputCommitTimer_.Stop()
				}
				timeout := uinputTxTimeout(e.uinputBsRtEwmaUsec_, rawLen)
				log.Printf("[uinputIM] macro tx %d armed: fallback commit in %v", txID, timeout)
				e.uinputCommitTimer_ = time.AfterFunc(timeout, func() {
					e.Lock()
					defer e.Unlock()
					if e.uinputTxID_ != txID || !e.uinputDeleting_ {
						return
					}
					e.uinputEndTxLocked(txID, "macro-timer")
				})
				uinputBackspace(rawLen)
			} else {
				e.commitText(macText)
			}
			return true, nil
		}
		return false, nil
	}

	// ── Enter / Return: commit composition or fast-path forward ──
	if keyVal == IBusReturn || keyVal == IBusKP_Enter {
		if e.getRawKeyLen() > 0 {
			log.Printf("[uinputIM] Enter: commit preedit=%q, forward to app", e.uinputPreeditString())
			e.resetPreedit()
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
		// T7: ALL transforms go through evdev BS + IBus CommitText. The
		// old uinputNoEcho_ ForwardKeyEvent(0x01000000+r) path is removed:
		// Chromium Wayland drops layout-less Unicode keysyms while
		// CommitText lands via text-input commit-string on every client.
		log.Printf("[uinputIM] tx start: uinput BS=%d, pending commit=%q", deleteLen, addPart)
		e.uinputBsSentAt_ = time.Now()
		e.uinputExpectingBs_ = deleteLen
		e.uinputSeenBs_ = 0
		e.uinputPendingCommit_ = addPart
		e.uinputDeleting_ = true
		e.uinputNoEcho_ = false
		e.uinputTxID_++
		txID := e.uinputTxID_
		if e.uinputCommitTimer_ != nil {
			e.uinputCommitTimer_.Stop()
		}
		timeout := uinputTxTimeout(e.uinputBsRtEwmaUsec_, deleteLen)
		log.Printf("[uinputIM] tx %d armed: fallback commit in %v", txID, timeout)
		e.uinputCommitTimer_ = time.AfterFunc(timeout, func() {
			e.Lock()
			defer e.Unlock()
			if e.uinputTxID_ != txID || !e.uinputDeleting_ {
				return
			}
			rt := time.Since(e.uinputBsSentAt_)
			log.Printf("[uinputIM] tx %d timer fire (seen=%d expect=%d, RT=%v): commit",
				txID, e.uinputSeenBs_, e.uinputExpectingBs_, rt)
			e.uinputEndTxLocked(txID, "timer")
		})
		uinputBackspace(deleteLen)
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
