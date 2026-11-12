package main

// Key reply ordering.
//
// GNOME Shell (and every IBus client) sends ProcessKeyEvent asynchronously
// and hands each key to the application when ITS reply arrives. godbus
// handles calls in parallel goroutines, and a key release returns at once
// while its press may wait for keyOrder, the engine lock or a transaction.
// The release reply could therefore overtake the press reply: the app saw
// "up" before "down", believed the key was held, and its client-side
// autorepeat deleted or moved the caret until another key was pressed (the
// stuck Backspace / Left arrow, worst for the keys Kernel mode injects with
// press and release 1ms apart).
//
// The incoming interceptor numbers every ProcessKeyEvent (press and release)
// in arrival order; the outgoing interceptor holds a key reply until every
// earlier key reply has been sent. A call that never gets a reply only
// delays later ones by kReplyOrderWait.

import (
	"log"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	kReplyOrderWait = 50 * time.Millisecond
	kKeyBarrierWait = 20 * time.Millisecond
)

type replyKey struct {
	peer   string
	serial uint32
}

type replyOrderer struct {
	mu      sync.Mutex
	cond    *sync.Cond
	issued  uint64
	sent    uint64 // every reply <= sent has been let through (or abandoned)
	pending map[replyKey]uint64
}

func newReplyOrderer() *replyOrderer {
	r := &replyOrderer{pending: map[replyKey]uint64{}}
	r.cond = sync.NewCond(&r.mu)
	return r
}

var replyOrder = newReplyOrderer()

// track registers a key call in arrival order (incoming interceptor) and
// returns its sequence number.
func (r *replyOrderer) track(k replyKey) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.issued++
	r.pending[k] = r.issued
	if len(r.pending) > 256 { // calls that never got a reply
		for pk, seq := range r.pending {
			if seq <= r.sent {
				delete(r.pending, pk)
			}
		}
	}
	return r.issued
}

// waitBefore blocks a key handler until the replies of every earlier key
// call are on their way out, so a CommitText/DeleteSurroundingText signal
// it emits cannot reach the client before an earlier key's reply (a
// forwarded character arriving after the text that should follow it).
// Bounded by kKeyBarrierWait.
func (r *replyOrderer) waitBefore(seq uint64) {
	if seq <= 1 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sent >= seq-1 {
		return
	}
	deadline := time.Now().Add(kKeyBarrierWait)
	timer := time.AfterFunc(kKeyBarrierWait, func() {
		r.mu.Lock()
		r.cond.Broadcast()
		r.mu.Unlock()
	})
	defer timer.Stop()
	for r.sent < seq-1 && time.Now().Before(deadline) {
		r.cond.Wait()
	}
}

// await blocks the reply to k until all earlier key replies are out.
func (r *replyOrderer) await(k replyKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seq, ok := r.pending[k]
	if !ok {
		return
	}
	delete(r.pending, k)
	if seq > r.sent+1 {
		deadline := time.Now().Add(kReplyOrderWait)
		timer := time.AfterFunc(kReplyOrderWait, func() {
			r.mu.Lock()
			r.cond.Broadcast()
			r.mu.Unlock()
		})
		for seq > r.sent+1 && time.Now().Before(deadline) {
			r.cond.Wait()
		}
		timer.Stop()
		if seq > r.sent+1 {
			log.Printf("[replyorder] reply %d: %d earlier key reply(s) missing, sending anyway", seq, seq-r.sent-1)
		}
	}
	if seq > r.sent {
		r.sent = seq
	}
	r.cond.Broadcast()
}

func isKeyEventCall(msg *dbus.Message) bool {
	if msg.Type != dbus.TypeMethodCall || len(msg.Body) != 3 {
		return false
	}
	if m, _ := msg.Headers[dbus.FieldMember].Value().(string); m != "ProcessKeyEvent" {
		return false
	}
	i, _ := msg.Headers[dbus.FieldInterface].Value().(string)
	return i == "org.freedesktop.IBus.Engine"
}

func headerString(msg *dbus.Message, f dbus.HeaderField) string {
	if v, ok := msg.Headers[f]; ok {
		s, _ := v.Value().(string)
		return s
	}
	return ""
}

// replyOrderOutgoing is the connection's outgoing interceptor.
func replyOrderOutgoing(msg *dbus.Message) {
	if msg.Type != dbus.TypeMethodReply && msg.Type != dbus.TypeError {
		return
	}
	v, ok := msg.Headers[dbus.FieldReplySerial]
	if !ok {
		return
	}
	serial, ok := v.Value().(uint32)
	if !ok {
		return
	}
	replyOrder.await(replyKey{headerString(msg, dbus.FieldDestination), serial})
}
