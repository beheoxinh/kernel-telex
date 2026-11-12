package main

/*
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>
#include <linux/input.h>
#include <linux/uinput.h>

static int uio_open(void) {
	return open("/dev/uinput", O_WRONLY);
}

static int uio_setup(int fd, const char *name) {
	struct uinput_setup usetup;
	memset(&usetup, 0, sizeof(usetup));
	strncpy(usetup.name, name, UINPUT_MAX_NAME_SIZE - 1);
	usetup.id.bustype = BUS_USB;
	usetup.id.vendor = 0x4242;
	usetup.id.product = 0x0001;
	return ioctl(fd, UI_DEV_SETUP, &usetup);
}

static int uio_set_evbit(int fd, int bit) {
	return ioctl(fd, UI_SET_EVBIT, bit);
}

static int uio_set_keybit(int fd, int bit) {
	return ioctl(fd, UI_SET_KEYBIT, bit);
}

static int uio_dev_create(int fd) {
	return ioctl(fd, UI_DEV_CREATE);
}

static int uio_dev_destroy(int fd) {
	return ioctl(fd, UI_DEV_DESTROY);
}

static void uio_ev(int fd, uint16_t type, uint16_t code, int32_t value) {
	struct input_event ev;
	memset(&ev, 0, sizeof(ev));
	ev.type = type;
	ev.code = code;
	ev.value = value;
	size_t written = 0;
	while (written < sizeof(ev)) {
		ssize_t ret = write(fd, ((const char*)&ev) + written, sizeof(ev) - written);
		if (ret > 0) {
			written += (size_t)ret;
		} else if (ret < 0 && errno == EINTR) {
			continue;
		} else {
			break;
		}
	}
}

static void uio_syn(int fd) {
	uio_ev(fd, EV_SYN, SYN_REPORT, 0);
}

static void uio_key(int fd, uint16_t code, int32_t value) {
	uio_ev(fd, EV_KEY, code, value);
	uio_syn(fd);
}

static void uio_tap(int fd, uint16_t code) {
	uio_key(fd, code, 1);
	usleep(1000);
	uio_key(fd, code, 0);
}

static void uio_backspace_batch(int fd, int n) {
	for (int i = 0; i < n; i++) {
		uio_tap(fd, KEY_BACKSPACE);
		if (i < n - 1) {
			usleep(1000);
		}
	}
}

// Select n characters left of the caret: Shift down, n x Left tap, Shift up.
static void uio_select_left_batch(int fd, int n) {
	uio_key(fd, KEY_LEFTSHIFT, 1);
	usleep(1000);
	for (int i = 0; i < n; i++) {
		uio_tap(fd, KEY_LEFT);
		usleep(1000);
	}
	uio_key(fd, KEY_LEFTSHIFT, 0);
}

static void uio_release_all(int fd) {
	uio_key(fd, KEY_LEFT, 0);
	uio_key(fd, KEY_BACKSPACE, 0);
	uio_key(fd, KEY_LEFTCTRL, 0);
	uio_key(fd, KEY_LEFTSHIFT, 0);
}

// Cached keyboard file descriptors for zero-overhead physical key state query.
// Scanning 64 /dev/input/event* devices from scratch on every rapid keystroke
// would touch audio/subsystem devices (e.g. HDMI, pcspkr) which have 10-15ms kernel probe
// latency, causing typing lag. Caching verified keyboard fds reduces latency to ~0.8 µs.
#define MAX_KBD_DEVICES 8
static int g_kbd_fds[MAX_KBD_DEVICES];
static int g_kbd_count = 0;
static int g_kbd_inited = 0;

static void uio_scan_keyboards(void) {
	// Static storage zero-initialises to 0, which is a *valid* fd (stdin).
	// Seed every slot to -1 once before the first close pass so the cleanup
	// below can never close a descriptor we did not open.
	if (!g_kbd_inited) {
		for (int i = 0; i < MAX_KBD_DEVICES; i++) {
			g_kbd_fds[i] = -1;
		}
		g_kbd_inited = 1;
	}
	// Close every slot we ever opened, not just [0, g_kbd_count): a count
	// that shrank (device unplugged mid-scan) used to leave the higher slots
	// open, and the next scan then leaked those descriptors.
	for (int i = 0; i < MAX_KBD_DEVICES; i++) {
		if (g_kbd_fds[i] >= 0) {
			close(g_kbd_fds[i]);
			g_kbd_fds[i] = -1;
		}
	}
	g_kbd_count = 0;
	char path[64];
	for (int i = 0; i < 32; i++) {
		snprintf(path, sizeof(path), "/dev/input/event%d", i);
		int fd = open(path, O_RDONLY | O_NONBLOCK);
		if (fd < 0) continue;

		char name[256] = {0};
		if (ioctl(fd, EVIOCGNAME(sizeof(name)), name) >= 0) {
			if (strstr(name, "Ktelex") != NULL) {
				close(fd);
				continue;
			}
		}

		// Check if device supports EV_KEY
		unsigned long ev_bits[EV_CNT / (sizeof(unsigned long) * 8) + 1] = {0};
		if (ioctl(fd, EVIOCGBIT(0, sizeof(ev_bits)), ev_bits) >= 0) {
			if (ev_bits[0] & (1UL << EV_KEY)) {
				// Must support both KEY_BACKSPACE and KEY_A (real keyboard, not audio or power button)
				unsigned long key_bits[KEY_CNT / (sizeof(unsigned long) * 8) + 1] = {0};
				if (ioctl(fd, EVIOCGBIT(EV_KEY, sizeof(key_bits)), key_bits) >= 0) {
					int bs_word = KEY_BACKSPACE / (sizeof(unsigned long) * 8);
					int bs_bit = KEY_BACKSPACE % (sizeof(unsigned long) * 8);
					int a_word = KEY_A / (sizeof(unsigned long) * 8);
					int a_bit = KEY_A % (sizeof(unsigned long) * 8);
					if ((key_bits[bs_word] & (1UL << bs_bit)) && (key_bits[a_word] & (1UL << a_bit))) {
						g_kbd_fds[g_kbd_count++] = fd;
						if (g_kbd_count >= MAX_KBD_DEVICES) break;
						continue;
					}
				}
			}
		}
		close(fd);
	}
}

// Queries physical keyboard devices to check if KEY_BACKSPACE is physically held down.
// Uses cached keyboard fds to achieve sub-microsecond response time.
// Returns 1 held, 0 released, -1 unknown (no readable keyboard). Rescans only
// when allow_scan is set. Callers serialise (Go physMu).
static int uio_backspace_state(int allow_scan) {
	if (g_kbd_count <= 0) {
		if (!allow_scan) return -1;
		uio_scan_keyboards();
		if (g_kbd_count <= 0) return -1;
	}
	int seen = 0;
	for (int i = 0; i < g_kbd_count; i++) {
		unsigned long key_state[KEY_CNT / (sizeof(unsigned long) * 8) + 1] = {0};
		if (ioctl(g_kbd_fds[i], EVIOCGKEY(sizeof(key_state)), key_state) >= 0) {
			int word = KEY_BACKSPACE / (sizeof(unsigned long) * 8);
			int bit = KEY_BACKSPACE % (sizeof(unsigned long) * 8);
			if (key_state[word] & (1UL << bit)) {
				return 1;
			}
			seen = 1;
		} else {
			// Device disconnected or invalidated -> trigger rescan on next query
			g_kbd_count = -1;
		}
	}
	return seen ? 0 : -1;
}
*/
import "C"

import (
	"fmt"
	"log"
	"sync"
	"syscall"
	"time"
)

var (
	uinputFd   C.int = -1
	uinputOnce sync.Once
	uinputDmu  sync.Mutex
)

func uinputDeviceOpen() (C.int, error) {
	fd := C.uio_open()
	if fd < 0 {
		return -1, fmt.Errorf("open /dev/uinput: %w", syscall.EPERM)
	}

	if C.uio_set_evbit(fd, C.EV_KEY) < 0 {
		C.close(fd)
		return -1, fmt.Errorf("set EV_KEY: %w", syscall.EIO)
	}
	needKeys := []C.int{
		C.KEY_BACKSPACE, C.KEY_LEFTCTRL, C.KEY_LEFTSHIFT, C.KEY_U,
		C.KEY_ENTER, C.KEY_SPACE, C.KEY_ESC, C.KEY_LEFT, C.KEY_RIGHT,
		C.KEY_UNKNOWN, // click-to-unstick tap (no keysym)
	}
	for _, k := range needKeys {
		if C.uio_set_keybit(fd, k) < 0 {
			C.close(fd)
			return -1, fmt.Errorf("set key %d: %w", int(k), syscall.EIO)
		}
	}
	for k := C.int(C.KEY_A); k <= C.int(C.KEY_Z); k++ {
		if C.uio_set_keybit(fd, k) < 0 {
			C.close(fd)
			return -1, fmt.Errorf("set key %d: %w", int(k), syscall.EIO)
		}
	}
	for k := C.int(C.KEY_1); k <= C.int(C.KEY_0); k++ {
		C.uio_set_keybit(fd, k)
	}

	if C.uio_setup(fd, C.CString("Ktelex-Uinput")) < 0 {
		C.close(fd)
		return -1, fmt.Errorf("uinput_setup: %w", syscall.EIO)
	}
	if C.uio_dev_create(fd) < 0 {
		C.close(fd)
		return -1, fmt.Errorf("dev_create: %w", syscall.EIO)
	}

	return fd, nil
}

// uinputDirectBackspace returns false when the in-process device is not
// open (the caller then falls back to the socket server).
func uinputDirectBackspace(n int) bool {
	debugf("[uinput] DIRECT_BS n=%d", n)
	uinputDmu.Lock()
	defer uinputDmu.Unlock()
	if uinputFd < 0 {
		return false
	}
	unstickNoteRisk()
	defer func() {
		// Fail-safe: guarantee KEY_BACKSPACE release event is always posted
		C.uio_key(uinputFd, C.KEY_BACKSPACE, 0)
	}()
	// Atomic tap batching: each backspace is sent as a full [press+syn+release+syn]
	// in a single write() call, eliminating any prolonged hold state where
	// compositor autorepeat could latch.
	C.uio_backspace_batch(uinputFd, C.int(n))
	return true
}

// uinputDirectSelectLeft selects n characters with Shift+Left. Returns false
// when the in-process device is not available (socket fallback cannot do it).
func uinputDirectSelectLeft(n int) bool {
	uinputDmu.Lock()
	defer uinputDmu.Unlock()
	if uinputFd < 0 {
		return false
	}
	debugf("[uinput] DIRECT_SELECT_LEFT n=%d", n)
	unstickNoteRisk()
	defer func() {
		C.uio_key(uinputFd, C.KEY_LEFT, 0)
		C.uio_key(uinputFd, C.KEY_LEFTSHIFT, 0)
	}()
	C.uio_select_left_batch(uinputFd, C.int(n))
	return true
}

// uinputDirectNudge taps KEY_UNKNOWN (no keysym): any key event ends an
// application's runaway key repeat. See clickunstick.go.
func uinputDirectNudge() bool {
	uinputDmu.Lock()
	defer uinputDmu.Unlock()
	if uinputFd < 0 {
		return false
	}
	C.uio_tap(uinputFd, C.KEY_UNKNOWN)
	return true
}

func uinputReleaseAll() {
	uinputDmu.Lock()
	defer uinputDmu.Unlock()
	if uinputFd >= 0 {
		C.uio_release_all(uinputFd)
	}
}

// isPhysicalBackspaceDown checks if KEY_BACKSPACE is physically held down on any real keyboard.
// Used by the anti-runaway watchdog to distinguish genuine user key holds (auto-repeat)
// from ghost/runaway repeat storms caused by compositor latches.
var isPhysicalBackspaceDownHook func() bool

// The C keyboard cache is shared by the key handlers and the click watcher
// goroutines: physMu serialises every access (a concurrent rescan could
// close a descriptor another goroutine just opened).
var (
	physMu         sync.Mutex
	physLastScan   time.Time
	physWarnedOnce sync.Once
)

const kPhysRescanGap = 2 * time.Second // between rescans while no keyboard is readable

// physBackspaceState: 1 held, 0 released, -1 unknown (keyboards unreadable,
// e.g. not yet in the input group until the next login).
func physBackspaceState() int {
	if isPhysicalBackspaceDownHook != nil {
		if isPhysicalBackspaceDownHook() {
			return 1
		}
		return 0
	}
	physMu.Lock()
	defer physMu.Unlock()
	allow := time.Since(physLastScan) >= kPhysRescanGap
	if allow {
		physLastScan = time.Now()
	}
	st := int(C.uio_backspace_state(C.int(boolInt(allow))))
	if st < 0 {
		physWarnedOnce.Do(func() {
			log.Printf("[uinput] cannot read keyboards (/dev/input/event*): Backspace guards disabled; log out and in after install")
		})
	}
	return st
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func isPhysicalBackspaceDown() bool { return physBackspaceState() == 1 }

// physBackspaceMaybeDown is true unless the key is known to be up: guards
// that swallow a Backspace must never do so when they cannot tell.
func physBackspaceMaybeDown() bool { return physBackspaceState() != 0 }

// physRescanKeyboards refreshes the keyboard cache (hotplug).
func physRescanKeyboards() {
	physMu.Lock()
	defer physMu.Unlock()
	C.uio_scan_keyboards()
	physLastScan = time.Now()
}

// uinputTryOpenDirect opens the in-process virtual keyboard.
func uinputTryOpenDirect() bool {
	fd, err := uinputDeviceOpen()
	if err != nil {
		log.Printf("[uinput] direct open failed: %v", err)
		return false
	}
	uinputDmu.Lock()
	uinputFd = fd
	uinputDmu.Unlock()
	log.Printf("[uinput] direct device opened (fd=%d)", int(fd))
	return true
}

// uinputInitDirect opens the device; when that fails (at boot /dev/uinput
// may get its ACL only after the session starts) it keeps retrying in the
// background.
func uinputInitDirect() {
	uinputOnce.Do(func() {
		unstickStart()
		if uinputTryOpenDirect() {
			return
		}
		go uinputOpenLoop(uinputTryOpenDirect, uinputNotifyUnavailable, uinputKick,
			kUinputRetryBase, kUinputRetryMax, nil)
	})
}
