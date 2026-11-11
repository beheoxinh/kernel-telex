package main

// Keystroke ordering.
//
// godbus runs every incoming method call in its own goroutine
// (conn.inWorker: `go conn.handleCall(msg)`), so two ProcessKeyEvent calls
// that arrive back to back — fast typing, key rollover, or a backlog after
// the process stalled — can enter the engine in the wrong order ("as" ->
// "sa", a tone landing on the previous syllable…).
//
// The incoming interceptor, however, runs synchronously in the reader
// goroutine, i.e. in arrival order. It hands every key press a ticket, and
// ProcessKeyEvent waits until all earlier tickets are done. Handlers cannot
// see their message, so tickets are matched by the (keyval, keycode, state)
// triple: events with the same triple are indistinguishable, so any of them
// may take any of their tickets. A ticket whose call never reaches the engine
// (e.g. sent to a destroyed engine object) is skipped after kKeyOrderWait.

import (
	"log"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const kKeyOrderWait = 40 * time.Millisecond

type keyTriple [3]uint32

type keyOrderer struct {
	mu      sync.Mutex
	cond    *sync.Cond
	issued  uint64
	served  uint64 // every ticket <= served is finished or abandoned
	done    map[uint64]bool
	pending map[keyTriple][]uint64
}

func newKeyOrderer() *keyOrderer {
	k := &keyOrderer{done: map[uint64]bool{}, pending: map[keyTriple][]uint64{}}
	k.cond = sync.NewCond(&k.mu)
	return k
}

var keyOrder = newKeyOrderer()

// issue registers a key press in arrival order (interceptor goroutine).
func (k *keyOrderer) issue(key keyTriple) {
	k.mu.Lock()
	k.issued++
	k.pending[key] = append(k.pending[key], k.issued)
	k.mu.Unlock()
}

// acquire blocks until every key press that arrived before this one has been
// processed (bounded by kKeyOrderWait). Returns 0 for untracked calls.
func (k *keyOrderer) acquire(key keyTriple) uint64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	var t uint64
	q := k.pending[key]
	for len(q) > 0 {
		t, q = q[0], q[1:]
		if t > k.served {
			break
		}
		t = 0 // abandoned ticket: drop it
	}
	if len(q) == 0 {
		delete(k.pending, key)
	} else {
		k.pending[key] = q
	}
	if t == 0 {
		return 0
	}
	if t > k.served+1 {
		deadline := time.Now().Add(kKeyOrderWait)
		timer := time.AfterFunc(kKeyOrderWait, func() {
			k.mu.Lock()
			k.cond.Broadcast()
			k.mu.Unlock()
		})
		for t > k.served+1 && time.Now().Before(deadline) {
			k.cond.Wait()
		}
		timer.Stop()
		if t > k.served+1 {
			log.Printf("[keyorder] ticket %d: %d earlier key(s) never arrived, skipping", t, t-k.served-1)
			k.served = t - 1
			k.advanceLocked()
		}
	}
	return t
}

// release marks a ticket finished and wakes the next key.
func (k *keyOrderer) release(t uint64) {
	if t == 0 {
		return
	}
	k.mu.Lock()
	if t > k.served {
		k.done[t] = true
		k.advanceLocked()
	}
	k.cond.Broadcast()
	k.mu.Unlock()
}

func (k *keyOrderer) advanceLocked() {
	for t := range k.done {
		if t <= k.served {
			delete(k.done, t)
		}
	}
	for k.done[k.served+1] {
		delete(k.done, k.served+1)
		k.served++
	}
}

// keyOrderInterceptor is installed as the IBus connection's incoming
// interceptor; it only tickets key presses (releases return immediately).
func keyOrderInterceptor(msg *dbus.Message) {
	if msg.Type != dbus.TypeMethodCall || len(msg.Body) != 3 {
		return
	}
	if m, _ := msg.Headers[dbus.FieldMember].Value().(string); m != "ProcessKeyEvent" {
		return
	}
	if i, _ := msg.Headers[dbus.FieldInterface].Value().(string); i != "org.freedesktop.IBus.Engine" {
		return
	}
	kv, ok1 := msg.Body[0].(uint32)
	kc, ok2 := msg.Body[1].(uint32)
	st, ok3 := msg.Body[2].(uint32)
	if !ok1 || !ok2 || !ok3 || st&IBusReleaseMask != 0 {
		return
	}
	keyOrder.issue(keyTriple{kv, kc, st})
}
