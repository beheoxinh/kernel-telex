package main

import (
	"ktelex/config"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BambooEngine/bamboo-core"
	"github.com/godbus/dbus/v5"
)

func (e *Engine) bsProcessKeyEvent(keyVal uint32, keyCode uint32, state uint32) (bool, *dbus.Error) {
	if isMovementKey(keyVal) {
		e.preeditor.Reset()
		e.isSurroundingTextReady = true
		return false, nil
	}
	var keyRune = rune(keyVal)
	if e.config.IBflags&config.IBmacroEnabled == 0 && len(keyPressChan) == 0 && e.getRawKeyLen() == 0 && !inKeyList(e.preeditor.GetInputMethod().AppendingKeys, keyRune) {
		e.updateLastKeyWithShift(keyVal, state)
		if e.preeditor.CanProcessKey(keyRune) && isValidState(state) {
			e.isFirstTimeSendingBS = true
			if state&IBusLockMask != 0 {
				keyRune = e.toUpper(keyRune)
			}
			e.preeditor.ProcessKey(keyRune, bamboo.VietnameseMode)
			e.bsCommitText([]rune(e.getPreeditString()))
			return true, nil
		}
		return false, nil
	}

	if e.shouldEnqueuKeyStrokes {
		// WARNING: don't use ForwardKeyEvent api in SurroundingText mode
		if e.checkInputMode(config.SurroundingTextIM) {
			if keyVal == IBusBackSpace {
				sleep()
				if e.getRawKeyLen() > 0 {
					if e.shouldFallbackToEnglish(true) {
						e.preeditor.RestoreLastWord(false)
					}
					e.preeditor.RemoveLastChar(false)
				}
				return false, nil
			}
			if keyVal == IBusTab {
				sleep()
				if ok, _ := e.getMacroText(); !ok {
					e.preeditor.Reset()
					return false, nil
				}
			}
			isValidKey := isValidState(state) && e.isValidKeyVal(keyVal)
			if !isValidKey {
				sleep()
				return e.keyPressHandler(keyVal, keyCode, state), nil
			}
		}
		// if the main thread is busy processing, the keypress events come all mixed up
		// so we enqueue these keypress events and process them sequentially on another thread
		keyPressChan <- [3]uint32{keyVal, keyCode, state}
		return true, nil
	} else {
		return e.keyPressHandler(keyVal, keyCode, state), nil
	}
}

func (e *Engine) keyPressForwardHandler(keyVal, keyCode, state uint32) {
	ret := e.keyPressHandler(keyVal, keyCode, state)
	if !ret {
		e.ForwardKeyEvent(keyVal, keyCode, state)
	}
}

func (e *Engine) keyPressHandler(keyVal, keyCode, state uint32) bool {
	defer e.updateLastKeyWithShift(keyVal, state)
	if e.keyPressDelay > 0 {
		time.Sleep(time.Duration(e.keyPressDelay) * time.Millisecond)
		e.keyPressDelay = 0
	}
	oldText := e.getPreeditString()
	_, oldMacText := e.getMacroText()
	if keyVal == IBusBackSpace {
		if e.getRawKeyLen() > 0 {
			if e.config.IBflags&config.IBautoNonVnRestore == 0 {
				e.preeditor.RemoveLastChar(false)
				return false
			}
			e.preeditor.RemoveLastChar(true)
			var newText = e.getPreeditString()
			var offset = e.getPreeditOffset([]rune(newText), []rune(oldText))
			if oldText != "" && offset != len([]rune(newText)) {
				e.updatePreviousText(oldText, newText)
				return true
			}
		}
		return false
	}

	if keyVal == IBusTab {
		defer e.preeditor.Reset()
		if oldMacText != "" {
			e.updatePreviousText(oldText, oldMacText)
			return true
		}
		return false
	}

	isValidKey := isValidState(state) && e.isValidKeyVal(keyVal)
	newText, isWordBreakRune := e.getCommitText(keyVal, keyCode, state)
	if len(newText) > 0 {
		if e.shouldAppendDeadKey(newText, oldText) {
			e.bsCommitText([]rune(" "))
			time.Sleep(10 * time.Millisecond)
			e.isFirstTimeSendingBS = false
			e.SendBackSpace(1)
		}
		e.updatePreviousTextInBatch(oldText, newText, isWordBreakRune)
		return isValidKey
	}
	return isValidKey
}

func (e *Engine) getPreeditOffset(newRunes, oldRunes []rune) int {
	var minLen = len(oldRunes)
	if len(newRunes) < minLen {
		minLen = len(newRunes)
	}
	for i := 0; i < minLen; i++ {
		if oldRunes[i] != newRunes[i] {
			return i
		}
	}
	return minLen
}

func (e *Engine) shouldAppendDeadKey(newText, oldText string) bool {
	var oldRunes = []rune(oldText)
	var newRunes = []rune(newText)
	var offset = e.getPreeditOffset(newRunes, oldRunes)

	// workaround for chrome and firefox's address bar
	if e.isFirstTimeSendingBS && offset < len(newRunes) && offset < len(oldRunes) && e.inBrowserList() {
		return true
	}
	return false
}

func (e *Engine) updatePreviousText(oldText, newText string) {
	offsetRunes, nBackSpace := e.getOffsetRunes(newText, oldText)
	if nBackSpace > 0 {
		e.SendBackSpace(nBackSpace)
	}
	log.Printf("Updating Previous Text %s ---> %s\n", oldText, newText)
	e.bsCommitText(offsetRunes)
}

func (e *Engine) updatePreviousTextInBatch(oldText, newText string, isWordBreakRune bool) {
	offsetRunes, nBackSpace := e.getOffsetRunes(newText, oldText)
	if nBackSpace > 0 {
		e.SendBackSpace(nBackSpace)
	}
	var buffer = []string{string(offsetRunes)}
	if isWordBreakRune {
		e.preeditor.Reset()
		buffer = append(buffer, "")
	}
	var isDirty = false
	for i := 0; i < len(keyPressChan); i++ {
		var keyEvents = <-keyPressChan
		var keyVal, keyCode, state = keyEvents[0], keyEvents[1], keyEvents[2]
		isValidKey := isValidState(state) && e.isValidKeyVal(keyVal)
		if isValidKey {
			var commitText, isWordBreakRune0 = e.getCommitText(keyVal, keyCode, state)
			buffer[len(buffer)-1] = commitText
			if isWordBreakRune0 {
				buffer = append(buffer, "")
			}
			isDirty = true
		} else {
			if isDirty {
				e.batchCommit(oldText, strings.Join(buffer, ""), nBackSpace, isWordBreakRune)
				buffer = []string{""}
			}
			e.ForwardKeyEvent(keyVal, keyCode, state)
		}
	}
	if isDirty {
		e.batchCommit(oldText, strings.Join(buffer, ""), nBackSpace, isWordBreakRune)
		return
	}
	log.Printf("Updating Previous Text %s ---> %s\n", oldText, newText)
	e.bsCommitText(offsetRunes)
}

func (e *Engine) batchCommit(oldText string, newText string, nBackSpace int, isWordBreakRune bool) {
	fullRunes := []rune(newText)
	if len(fullRunes) == 0 {
		return
	}
	patchedRunes, patchedBackSpace := e.getOffsetRunes(newText, oldText)
	if isWordBreakRune {
		e.bsCommitText(patchedRunes)
		return
	}
	if patchedBackSpace > nBackSpace {
		e.SendBackSpace(patchedBackSpace - nBackSpace)
	} else if patchedBackSpace < nBackSpace {
		var offset = utf8.RuneCountInString(oldText) - nBackSpace
		patchedRunes = fullRunes[offset:]
	}
	log.Printf("\nUpdating Previous Text %s ---> %s\n", oldText, newText)
	e.bsCommitText(patchedRunes)
}

func (e *Engine) getOffsetRunes(newText, oldText string) ([]rune, int) {
	var oldRunes = []rune(oldText)
	var newRunes = []rune(newText)
	var nBackSpace = 0
	var offset = e.getPreeditOffset(newRunes, oldRunes)
	if offset < len(oldRunes) {
		nBackSpace += len(oldRunes) - offset
	}
	return newRunes[offset:], nBackSpace
}

func (e *Engine) SendBackSpace(n int) {
	var now = time.Now()
	var delta = 50*1000*1000 - (now.UnixNano() - e.lastCommitText)
	if delta > 0 {
		time.Sleep(time.Duration(delta) * time.Nanosecond)
	}
	if e.checkInputMode(config.UinputIM) {
		log.Printf("Sendding %d backspace via Uinput\n", n)
		uinputBackspace(n)
		time.Sleep(time.Duration(n) * 20 * time.Millisecond)
	} else if e.checkInputMode(config.SurroundingTextIM) {
		time.Sleep(20 * time.Millisecond)
		log.Printf("Sendding %d backspace via SurroundingText\n", n)
		e.DeleteSurroundingText(-int32(n), uint32(n))
		time.Sleep(20 * time.Millisecond)
	} else {
		log.Printf("WARNING: unsupported input mode for SendBackSpace")
	}
}

func (e *Engine) bsCommitText(rs []rune) {
	if len(rs) == 0 {
		return
	}
	if e.checkInputMode(config.UinputIM) {
		log.Printf("Commit text [%s] via IBus (uinput mode)\n", string(rs))
		e.commitText(string(rs))
		return
	}
	e.commitText(string(rs))
}
