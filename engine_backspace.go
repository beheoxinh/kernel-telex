package main

import (
	"ktelex/config"
	"log"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/BambooEngine/bamboo-core"
	"github.com/godbus/dbus/v5"
)

type bsAction int

const (
	bsDone     bsAction = iota // handled, ret is the answer
	bsEnqueue                  // hand the key to the worker
	bsWaitIdle                 // wait for the worker to drain, then retry
)

// bsProcessKeyEvent is the SurroundingText-mode key path. It runs without
// the engine lock; every composer access happens under it, either here or
// in the worker, and a key handled here never overtakes a queued one.
func (e *Engine) bsProcessKeyEvent(keyVal uint32, keyCode uint32, state uint32) (bool, *dbus.Error) {
	if keyVal == IBusBackSpace || keyVal == IBusTab || isMovementKey(keyVal) {
		waitSurroundIdle()
	}
	for attempt := 0; ; attempt++ {
		e.Lock()
		idle := atomic.LoadInt32(&surroundInflight) == 0
		ret, act := e.bsKeyLocked(keyVal, keyCode, state, idle || attempt > 0)
		if act == bsWaitIdle {
			e.Unlock()
			waitSurroundIdle()
			continue
		}
		if act == bsEnqueue {
			atomic.AddInt32(&surroundInflight, 1)
		}
		e.Unlock()
		if act == bsEnqueue {
			startSurroundWorker()
			keyPressChan <- keyPressEvent{e, keyVal, keyCode, state}
			return true, nil
		}
		return ret, nil
	}
}

func (e *Engine) bsKeyLocked(keyVal, keyCode, state uint32, idle bool) (ret bool, act bsAction) {
	defer e.recoverKeyPanicLocked("surrounding-text key")
	if isMovementKey(keyVal) {
		e.preeditor.Reset()
		e.isSurroundingTextReady = true
		return false, bsDone
	}
	var keyRune = rune(keyVal)
	if e.config.IBflags&config.IBmacroEnabled == 0 && idle && atomic.LoadInt32(&surroundInflight) == 0 &&
		e.getRawKeyLen() == 0 && !inKeyList(e.preeditor.GetInputMethod().AppendingKeys, keyRune) {
		e.updateLastKeyWithShift(keyVal, state)
		if e.preeditor.CanProcessKey(keyRune) && isValidState(state) {
			e.isFirstTimeSendingBS = true
			if state&IBusLockMask != 0 {
				keyRune = e.toUpper(keyRune)
			}
			e.preeditor.ProcessKey(keyRune, bamboo.VietnameseMode)
			e.bsCommitText([]rune(e.getPreeditString()))
			return true, bsDone
		}
		return false, bsDone
	}

	if !e.shouldEnqueuKeyStrokes {
		return e.keyPressHandler(keyVal, keyCode, state), bsDone
	}
	// WARNING: don't use ForwardKeyEvent api in SurroundingText mode
	if e.checkInputMode(config.SurroundingTextIM) {
		if keyVal == IBusBackSpace {
			if e.getRawKeyLen() > 0 {
				if e.shouldFallbackToEnglish(true) {
					e.preeditor.RestoreLastWord(false)
				}
				e.preeditor.RemoveLastChar(false)
			}
			return false, bsDone
		}
		if keyVal == IBusTab {
			if ok, _ := e.getMacroText(); !ok {
				e.preeditor.Reset()
				return false, bsDone
			}
		}
		if !(isValidState(state) && e.isValidKeyVal(keyVal)) {
			if !idle {
				return false, bsWaitIdle
			}
			return e.keyPressHandler(keyVal, keyCode, state), bsDone
		}
	}
	// if the main thread is busy processing, the keypress events come all mixed up
	// so we enqueue these keypress events and process them sequentially on another thread
	return true, bsEnqueue
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
	debugf("Updating Previous Text %s ---> %s", oldText, newText)
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
	for {
		var ev keyPressEvent
		select {
		case ev = <-keyPressChan:
		default:
			goto drained
		}
		atomic.AddInt32(&surroundInflight, -1)
		if ev.e != e { // another engine's key: never run it through this one
			ev.e.ForwardKeyEvent(ev.keyVal, ev.keyCode, ev.state)
			continue
		}
		var keyVal, keyCode, state = ev.keyVal, ev.keyCode, ev.state
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
drained:
	if isDirty {
		e.batchCommit(oldText, strings.Join(buffer, ""), nBackSpace, isWordBreakRune)
		return
	}
	debugf("Updating Previous Text %s ---> %s", oldText, newText)
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
	debugf("Updating Previous Text %s ---> %s", oldText, newText)
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
		debugf("Commit text [%s] via IBus (uinput mode)", string(rs))
		e.commitText(string(rs))
		return
	}
	e.commitText(string(rs))
}
