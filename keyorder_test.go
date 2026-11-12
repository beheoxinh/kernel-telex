package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// Calls handled in reverse goroutine order must still be processed in
// arrival (ticket) order.
func TestKeyOrderSerializesOutOfOrderHandlers(t *testing.T) {
	k := newKeyOrderer()
	keys := []keyTriple{{'a', 1, 0}, {'s', 2, 0}, {'x', 3, 0}, {'a', 1, 0}}
	for _, key := range keys {
		k.issue(key, 0)
	}
	var mu sync.Mutex
	var got []uint32
	var wg sync.WaitGroup
	for i := len(keys) - 1; i >= 0; i-- { // start the handlers backwards
		wg.Add(1)
		go func(key keyTriple) {
			defer wg.Done()
			tk := k.acquire(key)
			mu.Lock()
			got = append(got, key[0])
			mu.Unlock()
			k.release(tk)
		}(keys[i])
		time.Sleep(2 * time.Millisecond)
	}
	wg.Wait()
	want := []uint32{'a', 's', 'x', 'a'}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("processing order %q, want %q", string(runeSlice(got)), string(runeSlice(want)))
		}
	}
}

func runeSlice(u []uint32) []rune {
	r := make([]rune, len(u))
	for i, v := range u {
		r[i] = rune(v)
	}
	return r
}

// A ticket whose call never arrives must not stall later keys for good.
func TestKeyOrderSkipsLostTicket(t *testing.T) {
	k := newKeyOrderer()
	k.issue(keyTriple{'q', 0, 0}, 0) // lost: never acquired
	k.issue(keyTriple{'w', 0, 0}, 0)
	k.issue(keyTriple{'e', 0, 0}, 0)
	start := time.Now()
	k.release(k.acquire(keyTriple{'w', 0, 0}))
	if d := time.Since(start); d > kKeyOrderWait+50*time.Millisecond {
		t.Fatalf("waited %v for a lost ticket", d)
	}
	start = time.Now()
	k.release(k.acquire(keyTriple{'e', 0, 0}))
	if d := time.Since(start); d > 10*time.Millisecond {
		t.Fatalf("second key still blocked %v after the skip", d)
	}
}

// Untracked calls (tests, other paths) never wait.
func TestKeyOrderUntrackedNoWait(t *testing.T) {
	k := newKeyOrderer()
	k.issue(keyTriple{'a', 0, 0}, 0)
	if tk := k.acquire(keyTriple{'b', 0, 0}); tk != 0 {
		t.Fatalf("untracked key got ticket %d", tk)
	}
}

func TestKeyOrderInterceptorFiltersMessages(t *testing.T) {
	saved := keyOrder
	keyOrder = newKeyOrderer()
	defer func() { keyOrder = saved }()
	mk := func(member, iface string, state uint32) *dbus.Message {
		return &dbus.Message{
			Type: dbus.TypeMethodCall,
			Headers: map[dbus.HeaderField]dbus.Variant{
				dbus.FieldMember:    dbus.MakeVariant(member),
				dbus.FieldInterface: dbus.MakeVariant(iface),
			},
			Body: []interface{}{uint32('a'), uint32(30), state},
		}
	}
	keyOrderInterceptor(mk("ProcessKeyEvent", "org.freedesktop.IBus.Engine", 0))
	keyOrderInterceptor(mk("ProcessKeyEvent", "org.freedesktop.IBus.Engine", IBusReleaseMask))
	keyOrderInterceptor(mk("FocusIn", "org.freedesktop.IBus.Engine", 0))
	if keyOrder.issued != 1 {
		t.Fatalf("issued=%d, want 1 (press only)", keyOrder.issued)
	}
}

// Password / PIN fields: keys pass straight through, nothing is composed.
func TestPasswordFieldPassthrough(t *testing.T) {
	e, fe := newUinputEchoEngine(t)
	e.SetContentType(ibusPurposePassword, 0)
	for _, k := range "as" {
		if ret, _ := e.ProcessKeyEvent(uint32(k), 0, 0); ret {
			t.Fatalf("password field: key %q consumed", k)
		}
	}
	if fe.commitText != "" || e.getRawKeyLen() != 0 {
		t.Fatalf("password field composed text: commit=%q raw=%d", fe.commitText, e.getRawKeyLen())
	}
	e.SetContentType(0, 0) // back to a normal field
	e.ProcessKeyEvent('a', 0, 0)
	if e.getRawKeyLen() == 0 {
		t.Fatalf("normal field after password: not composing")
	}
}

// Entering a password field drops the syllable typed before it.
func TestPasswordFieldDropsSyllable(t *testing.T) {
	e, _ := newUinputEchoEngine(t)
	e.ProcessKeyEvent('t', 0, 0)
	e.SetContentType(ibusPurposePin, 0)
	if e.getRawKeyLen() != 0 {
		t.Fatalf("syllable kept when entering a PIN field")
	}
}

func idle(e *Engine) {
	e.Lock()
	e.uinputLastKeyAt_ = time.Now().Add(-time.Second)
	e.uinputTxEndAt_ = time.Now().Add(-time.Second)
	e.Unlock()
}

// Surrounding text: caret moved away from the syllable (mouse click).
func TestSurroundingCaretMoveDropsSyllable(t *testing.T) {
	a := newAppModel(t)
	a.typeKey('t')
	a.typeKey('i')
	idle(a.e)
	a.e.uinputOnSurroundingText("hello ti", 8, 8) // still right after "ti"
	if a.e.getRawKeyLen() == 0 {
		t.Fatalf("syllable dropped although caret is still after it")
	}
	a.e.uinputOnSurroundingText("hello ti", 3, 3) // clicked into "hello"
	if a.e.getRawKeyLen() != 0 {
		t.Fatalf("syllable kept after caret moved")
	}
}

func TestSurroundingSelectionDropsSyllable(t *testing.T) {
	a := newAppModel(t)
	a.typeKey('t')
	idle(a.e)
	a.e.uinputOnSurroundingText("abc t", 5, 0)
	if a.e.getRawKeyLen() != 0 {
		t.Fatalf("syllable kept with a selection")
	}
}

// Right after a key the app may lag behind: never drop then.
func TestSurroundingIgnoredWhileTyping(t *testing.T) {
	a := newAppModel(t)
	a.typeKey('t')
	a.e.uinputOnSurroundingText("x", 1, 1)
	if a.e.getRawKeyLen() == 0 {
		t.Fatalf("syllable dropped by a stale surrounding update")
	}
}

// Reset() drops the syllable only for clients without surrounding text,
// and only when idle.
func TestResetDropsSyllableWhenIdle(t *testing.T) {
	a := newAppModel(t)
	a.typeKey('t')
	a.e.Reset() // right after typing: ignored
	if a.e.getRawKeyLen() == 0 {
		t.Fatalf("Reset right after a key dropped the syllable")
	}
	idle(a.e)
	a.e.Reset()
	if a.e.getRawKeyLen() != 0 {
		t.Fatalf("idle Reset kept the syllable")
	}

	b := newAppModel(t)
	b.typeKey('t')
	idle(b.e)
	b.e.uinputOnSurroundingText("t", 1, 1) // client supports surrounding text
	b.e.Reset()
	if b.e.getRawKeyLen() == 0 {
		t.Fatalf("Reset dropped the syllable although surrounding text says the caret is in place")
	}
}

func TestIbusTextString(t *testing.T) {
	v := dbus.MakeVariant([]interface{}{"IBusText", map[string]dbus.Variant{}, "xin chào", dbus.MakeVariant(0)})
	if s, ok := ibusTextString(v); !ok || s != "xin chào" {
		t.Fatalf("got %q,%v", s, ok)
	}
	if _, ok := ibusTextString(dbus.MakeVariant(uint32(1))); ok {
		t.Fatalf("non-IBusText accepted")
	}
}

func TestOpenLogFilePrivate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "k.log")
	if err := os.WriteFile(p, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	f, err := openLogFile(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if st, _ := os.Stat(p); st.Mode().Perm() != 0600 {
		t.Fatalf("log mode %v, want 0600", st.Mode().Perm())
	}
	link := filepath.Join(dir, "link.log")
	if err := os.Symlink(filepath.Join(dir, "target"), link); err != nil {
		t.Fatal(err)
	}
	if f, err := openLogFile(link); err == nil {
		f.Close()
		t.Fatalf("symlinked log path was followed")
	}
}
