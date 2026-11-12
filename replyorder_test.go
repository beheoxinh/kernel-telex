package main

import (
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func withFreshReplyOrder(t *testing.T) {
	t.Helper()
	savedR, savedK := replyOrder, keyOrder
	replyOrder, keyOrder = newReplyOrderer(), newKeyOrderer()
	t.Cleanup(func() { replyOrder, keyOrder = savedR, savedK })
}

func keyCall(state uint32, flags dbus.Flags) *dbus.Message {
	return &dbus.Message{
		Type:  dbus.TypeMethodCall,
		Flags: flags,
		Headers: map[dbus.HeaderField]dbus.Variant{
			dbus.FieldMember:    dbus.MakeVariant("ProcessKeyEvent"),
			dbus.FieldInterface: dbus.MakeVariant("org.freedesktop.IBus.Engine"),
			dbus.FieldSender:    dbus.MakeVariant(":1.7"),
		},
		Body: []interface{}{uint32(IBusBackSpace), uint32(14), state},
	}
}

// track registers serial s as the next key call (what the incoming
// interceptor does; Message.serial is not settable from outside godbus).
func track(s uint32) uint64 { return replyOrder.track(replyKey{":1.7", s}) }

func keyReply(serial uint32) *dbus.Message {
	return &dbus.Message{
		Type: dbus.TypeMethodReply,
		Headers: map[dbus.HeaderField]dbus.Variant{
			dbus.FieldDestination: dbus.MakeVariant(":1.7"),
			dbus.FieldReplySerial: dbus.MakeVariant(serial),
		},
	}
}

// A release answered at once must not overtake the reply to its press.
func TestReleaseReplyWaitsForPress(t *testing.T) {
	withFreshReplyOrder(t)
	track(10) // press
	track(11) // release

	var mu sync.Mutex
	var order []uint32
	sent := func(s uint32) {
		mu.Lock()
		order = append(order, s)
		mu.Unlock()
	}
	var wg sync.WaitGroup
	wg.Add(2)
	start := time.Now()
	go func() { // release handler: done immediately
		defer wg.Done()
		replyOrderOutgoing(keyReply(11))
		sent(11)
	}()
	go func() { // press handler: slow (lock, transaction…)
		defer wg.Done()
		time.Sleep(20 * time.Millisecond)
		replyOrderOutgoing(keyReply(10))
		sent(10)
	}()
	wg.Wait()
	if len(order) != 2 || order[0] != 10 || order[1] != 11 {
		t.Fatalf("reply order %v, want [10 11]", order)
	}
	if el := time.Since(start); el >= kReplyOrderWait {
		t.Fatalf("release waited %v, should only wait for the press", el)
	}
}

// A key call that never gets a reply delays later replies by a bounded time.
func TestReplyOrderMissingReplyIsBounded(t *testing.T) {
	withFreshReplyOrder(t)
	track(20)
	track(21)
	start := time.Now()
	replyOrderOutgoing(keyReply(21))
	el := time.Since(start)
	if el < kReplyOrderWait-5*time.Millisecond || el > kReplyOrderWait+100*time.Millisecond {
		t.Fatalf("waited %v, want about %v", el, kReplyOrderWait)
	}
	// The late reply goes straight out.
	start = time.Now()
	replyOrderOutgoing(keyReply(20))
	if el := time.Since(start); el > 5*time.Millisecond {
		t.Fatalf("late reply blocked %v", el)
	}
}

// Replies to other calls, signals and no-reply calls are never held.
func TestReplyOrderIgnoresOtherMessages(t *testing.T) {
	withFreshReplyOrder(t)
	keyOrderInterceptor(keyCall(0, dbus.FlagNoReplyExpected))
	keyOrderInterceptor(keyCall(IBusReleaseMask, 0))
	// The press still gets a processing ticket; only the release has a reply
	// to order (the no-reply press has none).
	if replyOrder.issued != 1 || keyOrder.issued != 1 {
		t.Fatalf("reply issued=%d key issued=%d, want 1/1",
			replyOrder.issued, keyOrder.issued)
	}
	replyOrder.sent = replyOrder.issued // that call is answered elsewhere
	start := time.Now()
	replyOrderOutgoing(keyReply(99)) // reply to e.g. FocusIn
	replyOrderOutgoing(&dbus.Message{Type: dbus.TypeSignal, Headers: map[dbus.HeaderField]dbus.Variant{}})
	if el := time.Since(start); el > 5*time.Millisecond {
		t.Fatalf("unrelated message blocked %v", el)
	}
}

// A key handler starts emitting text only after the previous key's reply
// has gone out (bounded when it never comes).
func TestKeyWaitsForPreviousReply(t *testing.T) {
	withFreshReplyOrder(t)
	s1 := track(30) // 'a' press: passed through, reply carries the char
	s2 := track(31) // 's' press: commits text
	var mu sync.Mutex
	var order []string
	note := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }
	done := make(chan struct{})
	go func() {
		replyOrder.waitBefore(s2)
		note("commit")
		close(done)
	}()
	time.Sleep(5 * time.Millisecond)
	note("reply")
	replyOrderOutgoing(keyReply(30))
	<-done
	if s1 != 1 || len(order) != 2 || order[0] != "reply" {
		t.Fatalf("order %v, want reply before commit", order)
	}
	// A reply that never comes only delays the next key by kKeyBarrierWait.
	s3 := track(40)
	_ = s3
	s4 := track(41)
	start := time.Now()
	replyOrder.waitBefore(s4)
	if el := time.Since(start); el < kKeyBarrierWait-5*time.Millisecond || el > kKeyBarrierWait+50*time.Millisecond {
		t.Fatalf("waited %v, want about %v", el, kKeyBarrierWait)
	}
}
