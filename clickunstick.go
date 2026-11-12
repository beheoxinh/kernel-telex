package main

// Click-to-unstick.
//
// On Wayland the application repeats a held key itself (GTK, Chromium, Qt).
// If it ever sees a Backspace/arrow press without its release, it keeps
// deleting or moving the caret until the next key event; a mouse click
// does not stop it. replyorder.go removes the known cause, this is the
// safety net: a mouse/touchpad button press shortly after Backspace,
// Delete or arrow activity makes the virtual keyboard tap KEY_UNKNOWN.
//
// Any key event stops the stuck repeat (GTK stops repeat on every key; a
// press+release of another key moves Chromium/Qt's repeat to that key and
// then ends it), and KEY_UNKNOWN has no keysym, so no application sees a
// character, shortcut or terminal escape sequence.
//
// Buttons are read straight from the pointer evdev nodes (read-only, the
// same access the engine already uses for the physical Backspace check).

import (
	"bufio"
	"encoding/binary"
	"log"
	"math/big"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	kUnstickRiskWindow = 30 * time.Second       // a click this long after BS/arrow activity nudges
	kUnstickMinGap     = 250 * time.Millisecond // between two nudges
	kUnstickRescan     = 10 * time.Second       // pointer hotplug

	evKey     = 0x01
	btnLeft   = 0x110
	btnRight  = 0x111
	btnMiddle = 0x112
	btnTouch  = 0x14a
)

var (
	unstickRiskAt  atomic.Int64 // unix nanos of the last BS/Delete/arrow press or injection
	unstickLastAt  atomic.Int64
	unstickOnce    sync.Once
	unstickMu      sync.Mutex
	unstickOpen    = map[string]bool{} // /dev/input/eventN being read
	unstickWarned  = map[string]bool{} // open errors already logged
	unstickDevList string              // last /proc/bus/input/devices, for hotplug
	unstickOff     bool                // set by tests: never read real devices
	unstickNudge   = uinputDirectNudge // hookable for tests
	unstickPhysBS  = isPhysicalBackspaceDown
	unstickTimeNow = time.Now
)

// unstickNoteRisk records activity that a stuck repeat could follow.
func unstickNoteRisk() { unstickRiskAt.Store(unstickTimeNow().UnixNano()) }

// unstickRiskKey reports whether a key press can leave a dangerous repeat.
func unstickRiskKey(keyVal uint32) bool {
	switch keyVal {
	case IBusBackSpace, IBusDelete, IBusLeft, IBusRight, IBusUp, IBusDown:
		return true
	}
	return false
}

// unstickOnPointerPress runs for every pointer button press.
func unstickOnPointerPress() bool {
	now := unstickTimeNow()
	risk := unstickRiskAt.Load()
	if risk == 0 || now.Sub(time.Unix(0, risk)) > kUnstickRiskWindow {
		return false
	}
	if last := unstickLastAt.Load(); last != 0 && now.Sub(time.Unix(0, last)) < kUnstickMinGap {
		return false
	}
	if unstickPhysBS() {
		return false // the user is really holding Backspace
	}
	if !unstickNudge() {
		return false
	}
	unstickLastAt.Store(now.UnixNano())
	unstickRiskAt.Store(0) // one nudge per episode of key activity
	debugf("[unstick] pointer click: KEY_UNKNOWN tap to end any stuck key repeat")
	return true
}

// unstickStart watches pointer devices for button presses (once).
func unstickStart() {
	if unstickOff {
		return // never read the real mouse from `go test`
	}
	unstickOnce.Do(func() {
		go func() {
			for {
				unstickScan()
				time.Sleep(kUnstickRescan)
			}
		}()
	})
}

func unstickScan() {
	data, err := os.ReadFile("/proc/bus/input/devices")
	if err != nil {
		return
	}
	// A keyboard plugged in after the first scan: refresh the cache the
	// physical-Backspace check uses.
	if list := string(data); list != unstickDevList {
		if unstickDevList != "" {
			physRescanKeyboards()
		}
		unstickDevList = list
	}
	for _, node := range pointerEventNodes(bufio.NewScanner(strings.NewReader(string(data)))) {
		unstickMu.Lock()
		busy := unstickOpen[node]
		if !busy {
			unstickOpen[node] = true
		}
		unstickMu.Unlock()
		if !busy {
			go unstickRead(node)
		}
	}
}

// pointerEventNodes parses /proc/bus/input/devices and returns the event
// nodes of devices with a left button (mice, touchpads, trackpoints),
// skipping our own virtual keyboard.
func pointerEventNodes(sc *bufio.Scanner) []string {
	var nodes []string
	var name, handler string
	var keys *big.Int
	flush := func() {
		if handler != "" && keys != nil && keys.Bit(btnLeft) == 1 && !strings.Contains(name, "Ktelex") {
			nodes = append(nodes, "/dev/input/"+handler)
		}
		name, handler, keys = "", "", nil
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "N: Name="):
			name = line
		case strings.HasPrefix(line, "H: Handlers="):
			for _, h := range strings.Fields(strings.TrimPrefix(line, "H: Handlers=")) {
				if strings.HasPrefix(h, "event") {
					handler = h
				}
			}
		case strings.HasPrefix(line, "B: KEY="):
			keys = parseEvBitmap(strings.TrimPrefix(line, "B: KEY="))
		}
	}
	flush()
	return nodes
}

// parseEvBitmap turns the space-separated hex words of a /proc bitmap
// (most significant word first, one unsigned long each) into a bit set.
func parseEvBitmap(s string) *big.Int {
	words := strings.Fields(s)
	v := new(big.Int)
	for _, w := range words {
		x, ok := new(big.Int).SetString(w, 16)
		if !ok {
			return nil
		}
		v.Lsh(v, 64)
		v.Or(v, x)
	}
	return v
}

func unstickRead(node string) {
	defer func() {
		unstickMu.Lock()
		delete(unstickOpen, node)
		unstickMu.Unlock()
	}()
	f, err := os.Open(node)
	if err != nil {
		unstickMu.Lock()
		warned := unstickWarned[node]
		unstickWarned[node] = true
		unstickMu.Unlock()
		if !warned {
			log.Printf("[unstick] %s: %v", node, err)
		}
		return
	}
	defer f.Close()
	debugf("[unstick] watching %s", node)
	const evSize = 24 // struct input_event on 64-bit: timeval(16) type(2) code(2) value(4)
	buf := make([]byte, evSize*64)
	for {
		n, err := f.Read(buf)
		if err != nil {
			return // unplugged: the next scan reopens it if it comes back
		}
		for off := 0; off+evSize <= n; off += evSize {
			typ := binary.LittleEndian.Uint16(buf[off+16:])
			code := binary.LittleEndian.Uint16(buf[off+18:])
			val := int32(binary.LittleEndian.Uint32(buf[off+20:]))
			if typ == evKey && val == 1 && isPointerButton(code) {
				unstickOnPointerPress()
			}
		}
	}
}

func isPointerButton(code uint16) bool {
	switch code {
	case btnLeft, btnRight, btnMiddle, btnTouch:
		return true
	}
	return false
}
