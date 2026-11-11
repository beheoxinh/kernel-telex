package main

import (
	"sync/atomic"
	"testing"
	"time"

	"ktelex/config"
)

// omniboxModel: an address bar that selects an inline completion after
// every typed character. Backspace with a completion shown only removes the
// completion; inserted text replaces it.
type omniboxModel struct {
	a       *appModel
	suggest bool
}

func (o *omniboxModel) fold() {
	a := o.a
	a.fe.mu.Lock()
	ct := a.fe.commitText
	a.fe.mu.Unlock()
	if len(ct) > len(a.seen) {
		a.buf = append(a.buf, []rune(ct[len(a.seen):])...)
		a.seen = ct
		o.suggest = false
	}
}

func (o *omniboxModel) key(k uint32) {
	a := o.a
	ret, _ := a.e.uinputProcessKeyEvent(k, k, 0)
	if !ret {
		if k == IBusBackSpace {
			if o.suggest {
				o.suggest = false
			} else if len(a.buf) > 0 {
				a.buf = a.buf[:len(a.buf)-1]
			}
		} else {
			a.buf = append(a.buf, rune(k))
			o.suggest = true
		}
	}
	o.fold()
}

func (o *omniboxModel) typeWord(word string) {
	a := o.a
	for _, k := range word {
		o.key(uint32(k))
		for {
			a.e.Lock()
			del, exp, seen := a.e.uinputDeleting_, a.e.uinputExpectingBs_, a.e.uinputSeenBs_
			a.e.Unlock()
			if !del {
				break
			}
			for i := seen; i < exp; i++ {
				o.key(IBusBackSpace)
			}
			a.awaitIdle(time.Second)
			o.fold()
		}
	}
}

func TestUrlFieldInlineCompletion(t *testing.T) {
	for word, want := range map[string]string{
		"as":      "á",
		"tieengs": "tiếng",
		"ddi":     "đi",
	} {
		o := &omniboxModel{a: newAppModel(t)}
		o.a.e.SetContentType(ibusPurposeUrl, 0)
		o.typeWord(word)
		if got := string(o.a.buf); got != want {
			t.Errorf("URL field %q: got %q, want %q", word, got, want)
		}
	}
	// Without the URL purpose the same field doubles the syllable: proves the
	// model reproduces the bug the fix is for.
	o := &omniboxModel{a: newAppModel(t)}
	o.typeWord("as")
	if got := string(o.a.buf); got == "á" {
		t.Fatalf("model does not reproduce the inline-completion bug")
	}
}

// URL fields keep the Shift+Left selection path: the leading " " keeps the
// field from becoming empty, and the selection covers it.
func TestUrlFieldSelectCoversSpace(t *testing.T) {
	saved := uinputSelectLeftHook
	var last int32
	uinputSelectLeftHook = func(n int) bool { atomic.StoreInt32(&last, int32(n)); return true }
	defer func() { uinputSelectLeftHook = saved }()
	a := newAppModel(t)
	a.e.config.UinputSelectApps = []string{"*"}
	a.e.SetContentType(ibusPurposeUrl, 0)
	a.e.uinputLastWm_ = "google-chrome"
	a.typeKey('a')
	a.typeKey('s')
	if n := atomic.LoadInt32(&last); n != 2 {
		t.Fatalf("selection of %d, want 2 (space + a)", n)
	}
}

// Browser address/search bars do not report purpose URL; the guard applies
// by WM class ("dd" in Thorium used to give "dđ").
func TestBrowserInlineCompletionByWm(t *testing.T) {
	for _, wm := range []string{"thorium-browser:Thorium-browser", "google-chrome:Google-chrome", "firefox:firefox"} {
		for word, want := range map[string]string{"dd": "đ", "ddi": "đi", "as": "á", "tieengs": "tiếng"} {
			o := &omniboxModel{a: newAppModel(t)}
			o.a.e.config.UinputSelectApps = []string{} // the model drives the BS path
			o.a.e.checkWmClass(wm)
			o.typeWord(word)
			if got := string(o.a.buf); got != want {
				t.Errorf("%s %q: got %q, want %q", wm, word, got, want)
			}
		}
	}
}

func TestUinputSelectDefaultToken(t *testing.T) {
	e, _ := newUinputEchoEngine(t)
	cases := []struct {
		apps []string
		wm   string
		want bool
	}{
		{nil, "discord", true},
		{nil, "code", false}, // VS Code: integrated terminal
		{[]string{"@default"}, "Slack", true},
		{[]string{"@default", "myapp"}, "myapp", true},
		{[]string{"myapp"}, "google-chrome", false},
		{[]string{}, "google-chrome", false},
	}
	for _, c := range cases {
		e.config.UinputSelectApps = c.apps
		if got := e.uinputUseSelect(c.wm); got != c.want {
			t.Errorf("apps=%v wm=%q: got %v want %v", c.apps, c.wm, got, c.want)
		}
	}
}

func TestConfigMarshalsDefaultToken(t *testing.T) {
	c := &config.Config{}
	b, err := c.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var back config.Config
	if err := back.UnmarshalJSON(b); err != nil {
		t.Fatal(err)
	}
	if len(back.UinputSelectApps) != 1 || back.UinputSelectApps[0] != config.UinputSelectDefaultToken {
		t.Fatalf("round trip gave %v", back.UinputSelectApps)
	}
}

func cleanEchoProfile(t *testing.T, wm string) {
	resetEchoProfile(wm)
	t.Cleanup(func() { resetEchoProfile(wm) })
}

// The watchdog of an app that echoes fast shrinks to its measured RT.
func TestEchoWaitAdapts(t *testing.T) {
	wm := "p2-fast-app"
	cleanEchoProfile(t, wm)
	if w := uinputEchoWait(wm); w != kUinputEchoWaitKnown {
		t.Fatalf("no samples: wait %v", w)
	}
	for i := 0; i < 8; i++ {
		uinputNoteEcho(wm, 10*time.Millisecond)
	}
	if w := uinputEchoWait(wm); w != kUinputEchoWaitMin {
		t.Fatalf("fast app: wait %v, want floor %v", w, kUinputEchoWaitMin)
	}
	slow := "p2-slow-app"
	cleanEchoProfile(t, slow)
	for i := 0; i < 8; i++ {
		uinputNoteEcho(slow, 120*time.Millisecond)
	}
	uinputNoteEcho(slow, 300*time.Millisecond) // jitter widens the wait
	w := uinputEchoWait(slow)
	if w <= 160*time.Millisecond || w > kUinputEchoWaitKnown {
		t.Fatalf("slow/jittery app: wait %v", w)
	}
	// Echoes without an RT sample (later echoes of a tx) do not count.
	other := "p2-norrt"
	cleanEchoProfile(t, other)
	for i := 0; i < 8; i++ {
		uinputNoteEcho(other, 0)
	}
	if w := uinputEchoWait(other); w != kUinputEchoWaitKnown {
		t.Fatalf("no RT samples: wait %v", w)
	}
}

// An app that echoed but now keeps missing is re-learned.
func TestEchoProfileDemotion(t *testing.T) {
	wm := "p2-demote"
	cleanEchoProfile(t, wm)
	uinputNoteEcho(wm, 5*time.Millisecond)
	for i := 0; i < kUinputEchoDemoteMiss-1; i++ {
		uinputNoteMissedEcho(wm)
	}
	if uinputEchoState(wm) != echoSeen {
		t.Fatalf("demoted too early")
	}
	uinputNoteMissedEcho(wm)
	if uinputEchoState(wm) != echoUnknown {
		t.Fatalf("not demoted after %d misses", kUinputEchoDemoteMiss)
	}
	// An echo in between resets the streak.
	uinputNoteEcho(wm, 5*time.Millisecond)
	uinputNoteMissedEcho(wm)
	uinputNoteEcho(wm, 0)
	for i := 0; i < kUinputEchoDemoteMiss-1; i++ {
		uinputNoteMissedEcho(wm)
	}
	if uinputEchoState(wm) != echoSeen {
		t.Fatalf("streak not reset by an echo")
	}
}

func TestUinputOpenLoopRetries(t *testing.T) {
	var calls, notices int32
	done := make(chan struct{})
	go func() {
		uinputOpenLoop(func() bool { return atomic.AddInt32(&calls, 1) >= 5 },
			func() { atomic.AddInt32(&notices, 1) }, nil,
			time.Millisecond, 4*time.Millisecond, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("open loop did not finish")
	}
	if calls != 5 || notices != 1 {
		t.Fatalf("calls=%d notices=%d, want 5 and 1", calls, notices)
	}
}

func TestUinputOpenLoopKickAndStop(t *testing.T) {
	kick := make(chan struct{}, 1)
	stop := make(chan struct{})
	var calls int32
	done := make(chan struct{})
	go func() {
		uinputOpenLoop(func() bool { atomic.AddInt32(&calls, 1); return false },
			nil, kick, time.Hour, time.Hour, stop)
		close(done)
	}()
	kick <- struct{}{}
	deadline := time.Now().Add(time.Second)
	for atomic.LoadInt32(&calls) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("kick did not trigger an attempt")
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("stop ignored")
	}
}
