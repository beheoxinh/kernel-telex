package main

/*
#include <stdint.h>
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
	uio_key(fd, code, 0);
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
	for i := 0; i < n; i++ {
		C.uio_key(uinputFd, C.KEY_BACKSPACE, 1)
		C.usleep(C.useconds_t(3000))
		C.uio_key(uinputFd, C.KEY_BACKSPACE, 0)
		C.usleep(C.useconds_t(1000))
	}
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
