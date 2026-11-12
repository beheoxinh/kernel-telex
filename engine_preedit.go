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
	"strings"
	"time"
	"unicode"

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
	if e.config.IBflags&config.IBspellCheckWithDicts != 0 && dictionaryOK.Load() {
		return !dictionary[vnSeq]
	}
	return !e.preeditor.IsValid(true) || !vnSyllableComplete(vnSeq)
}

// vnSyllableComplete applies the rules bamboo's validator lets through for a
// finished word: a syllable that ends in a stop (p, t, c, ch) carries sắc or
// nặng, and ă/â are always followed by a final consonant. "loop" -> "lôp",
// "keep" -> "kêp", "law" -> "lă" are English words, not Vietnamese.
func vnSyllableComplete(word string) bool {
	rs := []rune(word)
	// The last syllable: trailing letters.
	start := len(rs)
	for start > 0 && unicode.IsLetter(rs[start-1]) {
		start--
	}
	rs = rs[start:]
	lastVowel := -1
	tone := bamboo.ToneNone
	for i, c := range rs {
		if bamboo.IsVowel(c) {
			lastVowel = i
			if t := bamboo.FindToneFromChar(c); t != bamboo.ToneNone {
				tone = t
			}
		}
	}
	if lastVowel < 0 {
		return true
	}
	final := string(rs[lastVowel+1:])
	switch final {
	case "p", "t", "c", "ch":
		return tone == bamboo.ToneAcute || tone == bamboo.ToneDot
	case "":
		base := bamboo.AddToneToChar(rs[lastVowel], 0)
		return base != 'ă' && base != 'â'
	}
	return true
}

// shouldRestoreEnglish decides, at a word break, whether the word typed is
// English and its raw keys go back in place of the Vietnamese transform:
// "coffee" -> "coffee" not "cofê", "text" not "tẽt", "loop" not "lôp".
// A word without any Vietnamese letter whose last key undid the one before
// it ("ass" -> "as", "off" -> "of") is the Telex way of typing that literal
// text and is kept.
func (e *Engine) shouldRestoreEnglish() bool {
	if e.config.IBflags&config.IBautoNonVnRestore == 0 {
		return false
	}
	if ok, _ := e.getMacroText(); ok {
		return false
	}
	vn := e.getProcessedString(bamboo.VietnameseMode | bamboo.LowerCase)
	en := []rune(e.getProcessedString(bamboo.EnglishMode | bamboo.LowerCase))
	if vn == string(en) || !e.mustFallbackToEnglish() {
		return false
	}
	if bamboo.HasAnyVietnameseRune(vn) {
		return true
	}
	// Telex idiom: the last key undid the one before it ("ass" -> "as").
	n := len(en)
	return n < 2 || en[n-1] != en[n-2] || vn != string(en[:n-1])
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
	debugf("Commit Text [%s]", str)
	var now = time.Now()
	e.lastCommitText = now.UnixNano()
	e.CommitText(ibus.NewText(e.encodeText(str)))
}
