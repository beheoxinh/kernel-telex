package main

// UinputIM ("Kernel" mode): underline-free Vietnamese typing on Wayland.
//
// A transformation (e.g. "as" -> "á") is applied as a transaction:
//
//  1. the key is consumed and N characters are removed through the virtual
//     uinput keyboard (Backspace taps, or a Shift+Left selection for apps in
//     the select list);
//  2. those injected keys travel kernel -> compositor -> IBus and come back
//     to ProcessKeyEvent as "echoes"; each echo is forwarded so the app
//     performs the deletion/selection;
//  3. once every echo has passed, the replacement is sent with CommitText
//     after a short settle delay, then keys typed meanwhile are replayed.
//
// The commit MUST NOT reach the application before the deletion: both paths
// converge in the compositor, so a CommitText that overtakes a still queued
// Backspace lands first and is then erased by it (the lost-Unicode bug).
// Apps that are known to echo therefore get a long watchdog instead of a
// ~30ms fallback timer; only apps that never echo use the short timer.

import (
	"ktelex/config"
	"log"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/BambooEngine/bamboo-core"
	"github.com/godbus/dbus/v5"
)

const (
	kUinputEwmaAlpha    = 0.5
	kUinputMinUsec      = 10000                   // 10ms minimum commit delay after the last echo
	kUinputMaxUsec      = 30000                   // 30ms cap on the commit delay
	kUinputInitUsec     = 15000                   // 15ms initial EWMA seed
	kUinputStaleTimeout = 300 * time.Millisecond  // watchdog log threshold
	kUinputTxMinUsec    = 20000                   // 20ms base lower bound for the no-echo fallback timer
	kUinputTxMaxUsec    = 150000                  // 150ms upper bound for the no-echo fallback timer
	kUinputBsPeriodUsec = 4000                    // per injected key (tap + gap)
	kUinputTxSafetyUsec = 25000                   // safety margin for the no-echo fallback timer
	kUinputStrayBsGuard = 150 * time.Millisecond  // stray duplicate echo window after tx end
	kUinputGhostBsGuard = 1200 * time.Millisecond // loopback window measured from the last injection
	kUinputOwedExpiry   = 600 * time.Millisecond  // echo debt lifetime

	// Echo-capable apps: wait this long for the echo before committing anyway.
	// Once enough round trips are measured the wait adapts to the app
	// (srtt + 4*rttvar + margin, like a TCP RTO) within [min, known].
	kUinputEchoWaitKnown   = 400 * time.Millisecond
	kUinputEchoWaitMin     = 120 * time.Millisecond
	kUinputEchoWaitMargin  = 40 * time.Millisecond
	kUinputEchoMinSamples  = 4
	kUinputEchoDemoteMiss  = 3 // consecutive missed txs before a "seen" app is re-learned
	kUinputEchoWaitUnknown = 150 * time.Millisecond
	// Consecutive zero-echo transactions before an app is classed "never echoes".
	kUinputNoEchoStrikes = 2

	// Focus handling.
	kUinputFocusWait      = 250 * time.Millisecond // hold a commit while the text input is unfocused
	kUinputFocusSettle    = 15 * time.Millisecond  // delay after focus returns before committing
	kUinputFocusResetIdle = 600 * time.Millisecond // FocusIn this long after the last key = real focus move

	kUinputMaxDeferred = 64
)

// ── Per-app echo profile (shared by every engine instance) ──────────────

const (
	echoUnknown = 0
	echoSeen    = 1
	echoNever   = -1
)

type uinputEchoProfile struct {
	seen       bool
	strikes    int           // zero-echo txs while never seen
	missStreak int           // zero-echo txs in a row after having been seen
	samples    int           // measured first-echo round trips
	srtt       time.Duration // smoothed first-echo round trip
	rttvar     time.Duration // smoothed mean deviation
}

var (
	uinputEchoMu       sync.Mutex
	uinputEchoProfiles = map[string]*uinputEchoProfile{}
)

// echoProfileKey: apps are profiled by WM_CLASS. Without one (KDE and
// other compositors without a focus-tracking protocol, or a transient flap)
// all such windows share one profile, so the engine still learns whether
// echoes come back instead of always gambling on the short timer.
func echoProfileKey(wm string) string {
	if key := normalizeWmClass(wm); key != "" {
		return key
	}
	return "\x00no-wm-class"
}

func uinputEchoState(wm string) int {
	key := echoProfileKey(wm)
	uinputEchoMu.Lock()
	defer uinputEchoMu.Unlock()
	p := uinputEchoProfiles[key]
	switch {
	case p == nil:
		return echoUnknown
	case p.seen:
		return echoSeen
	case p.strikes >= kUinputNoEchoStrikes:
		return echoNever
	}
	return echoUnknown
}

// uinputNoteEcho records an echo; rt > 0 is the round trip of the first
// echo of a transaction (injection -> back in ProcessKeyEvent).
func uinputNoteEcho(wm string, rt time.Duration) {
	key := echoProfileKey(wm)
	uinputEchoMu.Lock()
	defer uinputEchoMu.Unlock()
	p := uinputEchoProfiles[key]
	if p == nil {
		p = &uinputEchoProfile{}
		uinputEchoProfiles[key] = p
	}
	p.seen = true
	p.strikes = 0
	p.missStreak = 0
	if rt <= 0 {
		return
	}
	if p.samples == 0 {
		p.srtt, p.rttvar = rt, rt/2
	} else {
		dev := p.srtt - rt
		if dev < 0 {
			dev = -dev
		}
		p.rttvar = (3*p.rttvar + dev) / 4
		p.srtt = (7*p.srtt + rt) / 8
	}
	p.samples++
}

// uinputEchoWait is how long to wait for the echoes of an app known to echo.
func uinputEchoWait(wm string) time.Duration {
	uinputEchoMu.Lock()
	defer uinputEchoMu.Unlock()
	p := uinputEchoProfiles[echoProfileKey(wm)]
	if p == nil || p.samples < kUinputEchoMinSamples {
		return kUinputEchoWaitKnown
	}
	w := p.srtt + 4*p.rttvar + kUinputEchoWaitMargin
	if w < kUinputEchoWaitMin {
		w = kUinputEchoWaitMin
	}
	if w > kUinputEchoWaitKnown {
		w = kUinputEchoWaitKnown
	}
	return w
}

func uinputNoteMissedEcho(wm string) {
	key := echoProfileKey(wm)
	uinputEchoMu.Lock()
	defer uinputEchoMu.Unlock()
	p := uinputEchoProfiles[key]
	if p == nil {
		p = &uinputEchoProfile{}
		uinputEchoProfiles[key] = p
	}
	if !p.seen {
		p.strikes++
		return
	}
	// An app that used to echo keeps missing: forget it and learn again
	// (focus moved to a part of the app that does not echo, new version…).
	p.missStreak++
	if p.missStreak >= kUinputEchoDemoteMiss {
		*p = uinputEchoProfile{}
	}
}

// ── Injection backends (hookable for tests) ─────────────────────────────

var uinputSelectLeftHook func(n int) bool

// uinputSelectLeft selects n characters left of the caret (Shift+Left).
// Returns false when the in-process device is unavailable.
func uinputSelectLeft(n int) bool {
	if uinputSelectLeftHook != nil {
		return uinputSelectLeftHook(n)
	}
	return uinputDirectSelectLeft(n)
}

// uinputUseSelect reports whether wm replaces text through a selection
// instead of Backspace. Selecting keeps the field non-empty, so rich editors
// (Gemini, ChatGPT, Messenger…) do not re-render their placeholder and drop
// the commit that follows a field-clearing Backspace.
func (e *Engine) uinputUseSelect(wm string) bool {
	norm := normalizeWmClass(wm)
	if norm == "" {
		return false
	}
	apps := e.config.UinputSelectApps
	if apps == nil {
		apps = []string{config.UinputSelectDefaultToken}
	}
	return uinputMatchApps(norm, apps, true)
}

func uinputMatchApps(norm string, apps []string, expand bool) bool {
	for _, app := range apps {
		app = strings.ToLower(strings.TrimSpace(app))
		if app == "" {
			continue
		}
		if app == config.UinputSelectDefaultToken {
			if expand && uinputMatchApps(norm, config.DefaultUinputSelectApps, false) {
				return true
			}
			continue
		}
		if strings.HasSuffix(app, "*") {
			if strings.HasPrefix(norm, strings.TrimSuffix(app, "*")) {
				return true
			}
		} else if norm == normalizeWmClass(app) {
			return true
		}
	}
	return false
}

// ── Helpers ─────────────────────────────────────────────────────────────

// uinputPreeditString returns the raw Vietnamese-mode preedit string,
// bypassing the shouldFallbackToEnglish validity check.
func (e *Engine) uinputPreeditString() string {
	return e.getProcessedString(bamboo.VietnameseMode)
}

// updateUinputEwma folds a measured echo round trip into the EWMA.
func (e *Engine) updateUinputEwma(rt time.Duration) {
	rtUsec := rt.Microseconds()
	if e.uinputBsRtEwmaUsec_ <= 0 || e.uinputBsRtEwmaUsec_ == kUinputInitUsec {
		e.uinputBsRtEwmaUsec_ = rtUsec
	} else {
		e.uinputBsRtEwmaUsec_ = int64(kUinputEwmaAlpha*float64(rtUsec) + (1.0-kUinputEwmaAlpha)*float64(e.uinputBsRtEwmaUsec_))
	}
}

// computeCommitDelay: settle delay between the last echo and CommitText so
// the app has processed the deletion before the replacement arrives.
func (e *Engine) computeCommitDelay(rt time.Duration) time.Duration {
	e.updateUinputEwma(rt)
	delayUsec := e.uinputBsRtEwmaUsec_
	if delayUsec < kUinputMinUsec {
		delayUsec = kUinputMinUsec
	}
	if delayUsec > kUinputMaxUsec {
		delayUsec = kUinputMaxUsec
	}
	return time.Duration(delayUsec) * time.Microsecond
}

// uinputTxTimeout is the short fallback for apps that never echo: injection
// time + measured RT + safety margin.
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

// uinputTxDeadline picks the watchdog for a transaction based on what we
// know about the app's echo behaviour.
func (e *Engine) uinputTxDeadline(wm string, deleteLen int) time.Duration {
	short := uinputTxTimeout(e.uinputBsRtEwmaUsec_, deleteLen)
	var wait time.Duration
	switch uinputEchoState(wm) {
	case echoNever:
		return short
	case echoSeen:
		wait = uinputEchoWait(wm)
	default:
		wait = kUinputEchoWaitUnknown
	}
	wait += time.Duration(deleteLen) * kUinputBsPeriodUsec * time.Microsecond
	if wait < short {
		return short
	}
	return wait
}

func (e *Engine) uinputStopTimerLocked() {
	if e.uinputCommitTimer_ != nil {
		e.uinputCommitTimer_.Stop()
		e.uinputCommitTimer_ = nil
	}
}

func (e *Engine) uinputTxOpenLocked() bool {
	return e.uinputDeleting_ || e.uinputPendingCommit_ != ""
}

// uinputResetTxStateLocked drops every piece of transaction state without
// committing (used by Disable).
func (e *Engine) uinputResetTxStateLocked() {
	e.uinputStopTimerLocked()
	e.uinputExpectingBs_ = 0
	e.uinputSeenBs_ = 0
	e.uinputDeleting_ = false
	e.uinputPendingCommit_ = ""
	e.uinputDeferredKeys_ = nil
	e.uinputDeferredCodes_ = nil
	e.uinputDeferredStates_ = nil
	e.uinputTxSelect_ = false
	e.uinputAwaitFocus_ = false
}

// ── Transaction lifecycle ───────────────────────────────────────────────

// uinputBeginTxLocked removes deleteLen characters before the caret and
// schedules commitText once the deletion has reached the app.
func (e *Engine) uinputBeginTxLocked(deleteLen int, commitText string, allowSelect bool) {
	wm := e.uinputLastWm_
	useSelect := allowSelect && e.uinputUseSelect(wm)

	// Address and search bars show an inline completion selected after the
	// caret ("d[antri.com.vn]"): the first Backspace (or Shift+Left) only
	// acts on that suggestion and the syllable comes out doubled ("dđ").
	// Committing a space first replaces the suggestion (or is simply
	// inserted), and one more key removes/selects it, so the count is right
	// whether or not a suggestion was shown, and the field never becomes
	// empty in select mode (space + "d" selected, then replaced by "đ").
	// The commit reaches the app before the injected key, which has to
	// travel kernel -> compositor -> IBus first.
	if e.inputPurpose_ == ibusPurposeUrl || e.uinputInlineCompletion_ ||
		uinputMatchApps(normalizeWmClass(wm), config.DefaultUinputCompletionApps, false) {
		e.commitText(" ")
		deleteLen++
	}

	e.uinputStopTimerLocked()
	e.uinputTxID_++
	txID := e.uinputTxID_
	e.uinputBsSentAt_ = time.Now()
	e.uinputExpectingBs_ = deleteLen
	e.uinputSeenBs_ = 0
	e.uinputPendingCommit_ = commitText
	e.uinputDeleting_ = true
	e.uinputTxWm_ = wm
	e.uinputTxSelect_ = useSelect
	e.uinputAwaitFocus_ = false

	timeout := e.uinputTxDeadline(wm, deleteLen)
	debugf("[uinputIM] tx %d start: del=%d commit=%q select=%v watchdog=%v", txID, deleteLen, commitText, useSelect, timeout)
	e.uinputCommitTimer_ = time.AfterFunc(timeout, func() {
		e.Lock()
		defer e.Unlock()
		if e.uinputTxID_ != txID || !e.uinputDeleting_ {
			return
		}
		log.Printf("[uinputIM] tx %d watchdog fire (seen=%d expect=%d, RT=%v, wm=%s)",
			txID, e.uinputSeenBs_, e.uinputExpectingBs_, time.Since(e.uinputBsSentAt_), e.uinputTxWm_)
		if e.uinputSeenBs_ == 0 {
			uinputNoteMissedEcho(e.uinputTxWm_)
		}
		e.uinputFinishTxLocked(txID, "timer")
	})

	if useSelect {
		if !uinputSelectLeft(deleteLen) {
			e.uinputTxSelect_ = false
			uinputBackspace(deleteLen)
		}
	} else {
		uinputBackspace(deleteLen)
	}
}

// uinputFinishTxLocked ends the tx unless the text input is momentarily
// unfocused (Chromium/Electron flap focus when an editor re-renders): a
// commit sent then is dropped, so it waits for FocusIn (bounded).
func (e *Engine) uinputFinishTxLocked(txID uint64, reason string) {
	if e.uinputTxID_ != txID || !e.uinputDeleting_ {
		return
	}
	if !e.uinputFocused_ && !e.uinputFocusOutAt_.IsZero() {
		if wait := kUinputFocusWait - time.Since(e.uinputFocusOutAt_); wait > 0 {
			debugf("[uinputIM] tx %d: input unfocused, hold commit up to %v", txID, wait)
			e.uinputAwaitFocus_ = true
			e.uinputStopTimerLocked()
			e.uinputCommitTimer_ = time.AfterFunc(wait, func() {
				e.Lock()
				defer e.Unlock()
				if e.uinputTxID_ != txID || !e.uinputDeleting_ {
					return
				}
				e.uinputEndTxLocked(txID, reason+"/focus-timeout")
			})
			return
		}
	}
	e.uinputEndTxLocked(txID, reason)
}

// uinputEndTxLocked finishes the active transaction: records echo debt,
// commits the replacement and replays deferred keys through the normal key
// logic (so a buffered tone key still transforms). Call with e.Mutex held.
func (e *Engine) uinputEndTxLocked(txID uint64, reason string) {
	if e.uinputTxID_ != txID {
		return
	}
	e.uinputStopTimerLocked()

	if e.uinputExpectingBs_ > e.uinputSeenBs_ {
		debt := e.uinputExpectingBs_ - e.uinputSeenBs_
		echoKey := uint32(IBusBackSpace)
		if e.uinputTxSelect_ {
			echoKey = IBusLeft
		}
		if e.uinputOwedBs_ > 0 && e.uinputOwedKey_ != echoKey {
			e.uinputOwedBs_ = 0
		}
		e.uinputOwedKey_ = echoKey
		e.uinputOwedBs_ += debt
		e.uinputOwedExpiresAt_ = time.Now().Add(kUinputOwedExpiry)
		log.Printf("[uinputIM] tx end (%s): echo debt=%d (total owed=%d)", reason, debt, e.uinputOwedBs_)
	}

	cText := e.uinputPendingCommit_
	keys := e.uinputDeferredKeys_
	codes := e.uinputDeferredCodes_
	states := e.uinputDeferredStates_
	e.uinputPendingCommit_ = ""
	e.uinputDeleting_ = false
	e.uinputExpectingBs_ = 0
	e.uinputSeenBs_ = 0
	e.uinputDeferredKeys_ = nil
	e.uinputDeferredCodes_ = nil
	e.uinputDeferredStates_ = nil
	e.uinputAwaitFocus_ = false
	e.uinputTxEndAt_ = time.Now()
	debugf("[uinputIM] tx end (%s): commit %q, replay %d keys", reason, cText, len(keys))
	if cText != "" {
		e.commitText(cText)
	}
	e.uinputReplayLocked(keys, codes, states)
}

// uinputReplayLocked feeds keys buffered during a transaction back through
// the key logic. Keys that would normally be forwarded raw are sent with
// ForwardKeyEvent; a key that opens a new transaction leaves the rest
// buffered on it.
func (e *Engine) uinputReplayLocked(keys, codes, states []uint32) {
	for i := range keys {
		if e.uinputDeleting_ {
			e.uinputDeferredKeys_ = append(e.uinputDeferredKeys_, keys[i:]...)
			e.uinputDeferredCodes_ = append(e.uinputDeferredCodes_, codes[i:]...)
			e.uinputDeferredStates_ = append(e.uinputDeferredStates_, states[i:]...)
			return
		}
		debugf("[uinputIM] replay buffered key 0x%04x", keys[i])
		if !e.uinputHandleKeyLocked(keys[i], codes[i], states[i], true) {
			e.ForwardKeyEvent(keys[i], codes[i], states[i])
			e.ForwardKeyEvent(keys[i], codes[i], states[i]|IBusReleaseMask)
		}
	}
}

// ── Focus ───────────────────────────────────────────────────────────────

// uinputOnFocusIn: a focus flap inside the same app keeps the transaction
// alive (ending it would commit before the queued deletion lands); a real
// app switch ends it; a FocusIn long after the last keystroke drops a stale
// syllable so the next key cannot delete text in the newly focused field.
func (e *Engine) uinputOnFocusIn(latestWm string) {
	e.Lock()
	defer e.Unlock()
	e.uinputFocused_ = true
	e.uinputSurroundSeen_ = false
	e.uinputInlineCompletion_ = false
	sameApp := latestWm == "" || e.uinputLastWm_ == "" || isSameWmClass(latestWm, e.uinputLastWm_)
	if e.uinputTxOpenLocked() {
		if !sameApp {
			e.uinputEndTxLocked(e.uinputTxID_, "focusin-app-change")
			return
		}
		if e.uinputAwaitFocus_ && e.uinputDeleting_ {
			txID := e.uinputTxID_
			e.uinputStopTimerLocked()
			e.uinputCommitTimer_ = time.AfterFunc(kUinputFocusSettle, func() {
				e.Lock()
				defer e.Unlock()
				if e.uinputTxID_ != txID || !e.uinputDeleting_ {
					return
				}
				e.uinputEndTxLocked(txID, "focus-restored")
			})
		}
		return
	}
	if !e.uinputLastKeyAt_.IsZero() && time.Since(e.uinputLastKeyAt_) > kUinputFocusResetIdle && e.getRawKeyLen() > 0 {
		debugf("[uinputIM] FocusIn after %v idle: drop stale syllable %q", time.Since(e.uinputLastKeyAt_), e.uinputPreeditString())
		e.preeditor.Reset()
	}
}

// ── Caret moves without keystrokes (mouse click, touchpad, app edits) ──
//
// The syllable lives only in the engine; if the caret moves, the next tone
// key would delete characters at the NEW position. Two signals:
//   - surrounding text (browsers, GTK, Qt): the text before the caret must
//     still end with the syllable, and there must be no selection;
//   - Reset() for clients that send no surrounding text.
// Both are ignored while a tx is open or right after a key / commit, when
// the app is still catching up with our own edits.

const kUinputCaretIdle = 150 * time.Millisecond

func (e *Engine) uinputCaretMayHaveMovedLocked() bool {
	if e.uinputTxOpenLocked() {
		return false
	}
	now := time.Now()
	if !e.uinputLastKeyAt_.IsZero() && now.Sub(e.uinputLastKeyAt_) < kUinputCaretIdle {
		return false
	}
	if !e.uinputTxEndAt_.IsZero() && now.Sub(e.uinputTxEndAt_) < kUinputCaretIdle {
		return false
	}
	return true
}

func (e *Engine) uinputOnReset() {
	e.Lock()
	defer e.Unlock()
	if e.uinputSurroundSeen_ || e.getRawKeyLen() == 0 || !e.uinputCaretMayHaveMovedLocked() {
		return // surrounding text decides, or nothing to drop
	}
	debugf("[uinputIM] Reset while idle: drop syllable")
	e.preeditor.Reset()
}

func (e *Engine) uinputOnSurroundingText(text string, cursor, anchor uint32) {
	e.Lock()
	defer e.Unlock()
	runes := []rune(text)
	if len(runes) == 0 || int(cursor) > len(runes) || int(anchor) > len(runes) {
		return // no usable information
	}
	e.uinputSurroundSeen_ = true
	if e.getRawKeyLen() == 0 {
		return
	}
	lo, hi := cursor, anchor
	if lo > hi {
		lo, hi = hi, lo
	}
	syllableEndsAt := func(pos uint32) bool {
		before := string(runes[:pos])
		for _, s := range []string{
			e.uinputPreeditString(),
			e.getPreeditString(),
			e.getProcessedString(bamboo.EnglishMode),
		} {
			if s != "" && strings.HasSuffix(before, e.encodeText(s)) {
				return true
			}
		}
		return false
	}
	// A selection that starts right after the syllable is an inline
	// completion (address bars, search boxes, LibreOffice word completion):
	// the next deletion must clear it first. Checked even right after a key,
	// which is exactly when completions appear.
	if lo != hi && syllableEndsAt(lo) {
		if !e.uinputInlineCompletion_ {
			debugf("[uinputIM] inline completion detected")
		}
		e.uinputInlineCompletion_ = true
		return
	}
	if !e.uinputCaretMayHaveMovedLocked() {
		return
	}
	moved := lo != hi || !syllableEndsAt(cursor) // a selection: typing replaces it
	if moved {
		debugf("[uinputIM] caret moved (surrounding text): drop syllable")
		e.preeditor.Reset()
	}
}

// ibusTextString extracts the string of a serialized IBusText
// ("IBusText", attachments, text, attrs).
func ibusTextString(v dbus.Variant) (s string, ok bool) {
	defer func() {
		if recover() != nil {
			s, ok = "", false
		}
	}()
	rv := reflect.ValueOf(v.Value())
	if rv.Kind() != reflect.Slice || rv.Len() < 3 {
		return "", false
	}
	s, ok = rv.Index(2).Interface().(string)
	return s, ok
}

func (e *Engine) uinputOnFocusOut() {
	e.Lock()
	e.uinputFocused_ = false
	e.uinputFocusOutAt_ = time.Now()
	e.Unlock()
	// Make sure no virtual key stays pressed (autorepeat latch).
	uinputReleaseAll()
}

// ── Key handling ────────────────────────────────────────────────────────

// uinputProcessKeyEvent handles UinputIM key events.
//
//   - Simple append (old+char==new): forward raw (return false), zero latency.
//   - Transformation: consume, open a transaction (see file header).
//   - Echoes of injected keys are forwarded and counted.
//   - Keys arriving while a transaction is open are buffered and replayed.
//   - Ctrl/Alt+letter, navigation and user Backspace are forwarded.
func (e *Engine) uinputProcessKeyEvent(keyVal uint32, keyCode uint32, state uint32) (bool, *dbus.Error) {
	e.Lock()
	defer e.Unlock()

	// Late echoes of a transaction that already ended (watchdog fired before
	// they arrived) and stray duplicates.
	if !e.uinputDeleting_ && e.uinputOwedBs_ > 0 && keyVal == e.uinputOwedKey_ {
		if time.Now().Before(e.uinputOwedExpiresAt_) && (keyVal != IBusBackSpace || !isPhysicalBackspaceDown()) {
			e.uinputOwedBs_--
			// A late echo: teach the profile how slow the app really is.
			uinputNoteEcho(e.uinputTxWm_, time.Since(e.uinputBsSentAt_))
			log.Printf("[uinputIM] swallow late echo 0x%04x (remaining debt=%d)", keyVal, e.uinputOwedBs_)
			return true, nil
		}
		e.uinputOwedBs_ = 0
	}
	if keyVal == IBusBackSpace && !e.uinputDeleting_ && !isPhysicalBackspaceDown() {
		if !e.uinputTxEndAt_.IsZero() && time.Since(e.uinputTxEndAt_) < kUinputStrayBsGuard {
			log.Printf("[uinputIM] stray BS echo after tx end: swallow")
			return true, nil
		}
		// A genuine user Backspace (incl. autorepeat) always has the physical
		// key down; "recent injection + key up" is a compositor loopback.
		if !e.uinputBsSentAt_.IsZero() && time.Since(e.uinputBsSentAt_) < kUinputGhostBsGuard {
			log.Printf("[uinputIM] ghost/loopback BS swallowed (%v after injection)", time.Since(e.uinputBsSentAt_))
			return true, nil
		}
	}
	e.uinputLastKeyAt_ = time.Now()

	wm := e.getWmClass()
	if e.uinputLastWm_ != "" && wm != "" && !isSameWmClass(wm, e.uinputLastWm_) {
		if e.uinputTxOpenLocked() {
			log.Printf("[uinputIM] wm change %s -> %s with open tx: end tx", e.uinputLastWm_, wm)
			e.uinputEndTxLocked(e.uinputTxID_, "wm-change")
		}
		if e.getRawKeyLen() > 0 {
			debugf("[uinputIM] app change: reset preedit")
			e.preeditor.Reset()
		}
	}
	if wm != "" {
		e.uinputLastWm_ = wm
	}

	if e.uinputDeleting_ {
		echoKey := uint32(IBusBackSpace)
		if e.uinputTxSelect_ {
			echoKey = IBusLeft
		}
		if keyVal == echoKey && !(keyVal == IBusBackSpace && isPhysicalBackspaceDown()) {
			e.uinputSeenBs_++
			var rt time.Duration
			if e.uinputSeenBs_ == 1 {
				rt = time.Since(e.uinputBsSentAt_)
			}
			uinputNoteEcho(e.uinputTxWm_, rt)
			debugf("[uinputIM] echo %d/%d", e.uinputSeenBs_, e.uinputExpectingBs_)
			if e.uinputSeenBs_ >= e.uinputExpectingBs_ {
				rt := time.Since(e.uinputBsSentAt_)
				delay := e.computeCommitDelay(rt)
				txID := e.uinputTxID_
				e.uinputStopTimerLocked()
				debugf("[uinputIM] all echoes in, RT=%v, commit in %v", rt, delay)
				e.uinputCommitTimer_ = time.AfterFunc(delay, func() {
					e.Lock()
					defer e.Unlock()
					e.uinputFinishTxLocked(txID, "all-echo")
				})
			}
			return false, nil
		}
		if keyVal >= 0xffe1 && keyVal <= 0xfff0 { // modifiers
			return false, nil
		}
		if e.uinputExpectingBs_ > 0 && time.Since(e.uinputBsSentAt_) > kUinputStaleTimeout {
			debugf("[uinputIM] no echo progress (seen=%d expect=%d)", e.uinputSeenBs_, e.uinputExpectingBs_)
		}
		if len(e.uinputDeferredKeys_) < kUinputMaxDeferred {
			debugf("[uinputIM] buffer key 0x%04x during tx", keyVal)
			e.uinputDeferredKeys_ = append(e.uinputDeferredKeys_, keyVal)
			e.uinputDeferredCodes_ = append(e.uinputDeferredCodes_, keyCode)
			e.uinputDeferredStates_ = append(e.uinputDeferredStates_, state)
		} else {
			log.Printf("[uinputIM] deferred buffer full, dropping key 0x%04x", keyVal)
		}
		return true, nil
	}

	defer e.updateLastKeyWithShift(keyVal, state)
	return e.uinputHandleKeyLocked(keyVal, keyCode, state, false), nil
}

// uinputHandleKeyLocked runs the composition logic for one key outside a
// transaction. Returns true when the key is consumed, false when the key
// itself must reach the app. replay=true for keys replayed after a tx.
func (e *Engine) uinputHandleKeyLocked(keyVal, keyCode, state uint32, replay bool) bool {
	keyRune := rune(keyVal)

	if state&(IBusControlMask|IBusMod1Mask) != 0 && keyRune >= 'a' && keyRune <= 'z' {
		return false
	}

	if keyVal == IBusBackSpace {
		if !replay && e.uinputRunawayBsLocked() {
			return true
		}
		if e.getRawKeyLen() > 0 {
			e.preeditor.RemoveLastChar(true)
			debugf("[uinputIM] BS: removeLastChar -> %q", e.uinputPreeditString())
		}
		return false
	}

	if !e.shouldRestoreKeyStrokes {
		if !e.preeditor.CanProcessKey(keyRune) && e.getRawKeyLen() == 0 && e.config.IBflags&config.IBmacroEnabled == 0 {
			return false
		}
	}

	if keyVal == IBusTab {
		if ok, macText := e.getMacroText(); ok {
			if rawLen := utf8.RuneCountInString(e.uinputPreeditString()); rawLen > 0 {
				e.preeditor.Reset()
				e.uinputBeginTxLocked(rawLen, macText, true)
			} else {
				e.commitText(macText)
			}
			return true
		}
		return false
	}

	if keyVal == IBusReturn || keyVal == IBusKP_Enter {
		if e.getRawKeyLen() > 0 {
			e.resetPreedit()
		}
		return false
	}

	if bamboo.IsWordBreakSymbol(keyRune) {
		if e.getRawKeyLen() > 0 {
			e.resetPreedit()
		}
		return false
	}

	if !e.isPrintableKey(state, keyVal) {
		if e.getRawKeyLen() > 0 {
			debugf("[uinputIM] funct key 0x%04x: reset preedit", keyVal)
			e.resetPreedit()
		}
		return false
	}

	oldText := e.uinputPreeditString()
	keyS := string(keyRune)
	e.preeditor.ProcessKey(keyRune, bamboo.VietnameseMode)
	afterText := e.uinputPreeditString()

	if oldText+keyS == afterText {
		debugf("[uinputIM] %q + %q: simple append", oldText, keyS)
		return false
	}

	oldRunes := []rune(oldText)
	afterRunes := []rune(afterText)
	pfx := runePrefixLen(oldText, afterText)
	deleteLen := len(oldRunes) - pfx
	addPart := string(afterRunes[pfx:])
	debugf("[uinputIM] transform %q + %q -> %q (pfx=%d del=%d add=%q)", oldText, keyS, afterText, pfx, deleteLen, addPart)

	if deleteLen > 0 {
		// Selecting is only needed when the whole syllable is replaced: that
		// is the only case where Backspace can empty the field.
		e.uinputBeginTxLocked(deleteLen, addPart, pfx == 0)
	} else if addPart != "" {
		e.commitText(addPart)
	} else {
		log.Printf("[uinputIM] WARNING: transform with nothing to delete or add")
	}
	return true
}

// uinputRunawayBsLocked is the anti-runaway watchdog for idle Backspace:
// bursts faster than a human can tap with the physical key UP are a
// compositor autorepeat latch and are swallowed.
func (e *Engine) uinputRunawayBsLocked() bool {
	now := time.Now()
	interval := now.Sub(e.uinputLastIdleBsAt_)
	e.uinputLastIdleBsAt_ = now
	if interval < 45*time.Millisecond || (e.uinputIdleBsBurstCount_ >= 2 && interval < 120*time.Millisecond) {
		e.uinputIdleBsBurstCount_++
		if e.uinputIdleBsBurstCount_ >= 2 && !isPhysicalBackspaceDown() {
			uinputReleaseAll()
			log.Printf("[uinputIM] RUNAWAY BS STORM (burst=%d, intv=%v): swallowed", e.uinputIdleBsBurstCount_, interval)
			return true
		}
	} else {
		e.uinputIdleBsBurstCount_ = 1
	}
	return false
}
