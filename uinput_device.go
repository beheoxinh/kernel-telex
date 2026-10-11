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

static void uio_release_all(int fd) {
	uio_key(fd, KEY_BACKSPACE, 0);
	uio_key(fd, KEY_LEFTCTRL, 0);
	uio_key(fd, KEY_LEFTSHIFT, 0);
}

// Queries all physical keyboard event devices to check if KEY_BACKSPACE is physically held down.
// Ignores the virtual Ktelex-Uinput device.
// Returns 1 if physically held, 0 if released or on error.
static int uio_is_physical_backspace_down(void) {
	char path[64];
	for (int i = 0; i < 64; i++) {
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

		unsigned long key_state[KEY_CNT / (sizeof(unsigned long) * 8) + 1] = {0};
		if (ioctl(fd, EVIOCGKEY(sizeof(key_state)), key_state) >= 0) {
			int word = KEY_BACKSPACE / (sizeof(unsigned long) * 8);
			int bit = KEY_BACKSPACE % (sizeof(unsigned long) * 8);
			if (key_state[word] & (1UL << bit)) {
				close(fd);
				return 1;
			}
		}
		close(fd);
	}
	return 0;
}
*/
import "C"

import (
	"fmt"
	"log"
	"sync"
	"syscall"
)

var (
	uinputFd   C.int = -1
	uinputOnce  sync.Once
	uinputDmu   sync.Mutex
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
		C.KEY_ENTER, C.KEY_SPACE, C.KEY_ESC,
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

func uinputDeviceClose() {
	uinputDmu.Lock()
	defer uinputDmu.Unlock()
	if uinputFd >= 0 {
		C.uio_dev_destroy(uinputFd)
		C.close(uinputFd)
		uinputFd = -1
	}
}

func uinputDirectTap(code int) {
	uinputDmu.Lock()
	defer uinputDmu.Unlock()
	if uinputFd >= 0 {
		C.uio_tap(uinputFd, C.uint16_t(code))
	}
}

func uinputDirectBackspace(n int) {
	log.Printf("[uinput] DIRECT_BS n=%d", n)
	uinputDmu.Lock()
	defer uinputDmu.Unlock()
	if uinputFd < 0 {
		return
	}
	defer func() {
		// Fail-safe: guarantee KEY_BACKSPACE release event is always posted
		C.uio_key(uinputFd, C.KEY_BACKSPACE, 0)
	}()
	// Atomic tap batching: each backspace is sent as a full [press+syn+release+syn]
	// in a single write() call, eliminating any prolonged hold state where
	// compositor autorepeat could latch.
	C.uio_backspace_batch(uinputFd, C.int(n))
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

func isPhysicalBackspaceDown() bool {
	if isPhysicalBackspaceDownHook != nil {
		return isPhysicalBackspaceDownHook()
	}
	return C.uio_is_physical_backspace_down() != 0
}

func uinputInitDirect() {
	uinputOnce.Do(func() {
		fd, err := uinputDeviceOpen()
		if err != nil {
			log.Printf("[uinput] direct open failed: %v (falling back to socket)", err)
			uinputFd = -1
			uinputInit() // fallback to socket-based
			return
		}
		uinputFd = fd
		log.Printf("[uinput] direct device opened (fd=%d)", int(fd))
	})
}
