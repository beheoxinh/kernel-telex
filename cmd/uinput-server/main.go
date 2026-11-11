package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

/*
#include <stdint.h>
#include <string.h>
#include <stdlib.h>
#include <unistd.h>
#include <fcntl.h>
#include <linux/input.h>
#include <linux/uinput.h>

static int uio_open(void) {
	return open("/dev/uinput", O_WRONLY | O_NONBLOCK);
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
	write(fd, &ev, sizeof(ev));
}

static void uio_syn(int fd) {
	uio_ev(fd, EV_SYN, SYN_REPORT, 0);
}

static void uio_key(int fd, uint16_t code, int32_t value) {
	uio_ev(fd, EV_KEY, code, value);
	uio_syn(fd);
}

static void uio_tap(int fd, uint16_t code) {
	struct input_event ev[3];
	memset(ev, 0, sizeof(ev));
	ev[0].type = EV_KEY; ev[0].code = code; ev[0].value = 1; // press
	ev[1].type = EV_KEY; ev[1].code = code; ev[1].value = 0; // release
	ev[2].type = EV_SYN; ev[2].code = SYN_REPORT;             // sync
	write(fd, ev, sizeof(ev));
}
*/
import "C"

const (
	sockAddr = "ktelex-uinput"
)

type UinputServer struct {
	fd       C.int
	done     chan struct{}
	packetMu sync.Mutex
}

func NewUinputServer() (*UinputServer, error) {
	fd := C.uio_open()
	if fd < 0 {
		return nil, fmt.Errorf("open /dev/uinput: %w", syscall.EPERM)
	}

	if C.uio_set_evbit(fd, C.EV_KEY) < 0 {
		C.close(fd)
		return nil, fmt.Errorf("set EV_KEY: %w", syscall.EIO)
	}
	needKeys := []C.int{
		C.KEY_BACKSPACE, C.KEY_LEFTCTRL, C.KEY_LEFTSHIFT, C.KEY_U, C.KEY_ENTER, C.KEY_SPACE, C.KEY_ESC,
	}
	for _, k := range needKeys {
		if C.uio_set_keybit(fd, k) < 0 {
			C.close(fd)
			return nil, fmt.Errorf("set special key %d: %w", int(k), syscall.EIO)
		}
	}

	// a-z
	for k := C.int(C.KEY_A); k <= C.int(C.KEY_Z); k++ {
		if C.uio_set_keybit(fd, k) < 0 {
			C.close(fd)
			return nil, fmt.Errorf("set key %d: %w", int(k), syscall.EIO)
		}
	}
	// 0-9
	for k := C.int(C.KEY_1); k <= C.int(C.KEY_0); k++ {
		C.uio_set_keybit(fd, k)
	}

	if C.uio_setup(fd, C.CString("Ktelex-Uinput")) < 0 {
		C.close(fd)
		return nil, fmt.Errorf("uinput_setup: %w", syscall.EIO)
	}
	if C.uio_dev_create(fd) < 0 {
		C.close(fd)
		return nil, fmt.Errorf("dev_create: %w", syscall.EIO)
	}

	s := &UinputServer{fd: fd, done: make(chan struct{})}
	return s, nil
}

func (s *UinputServer) Close() {
	if s.fd >= 0 {
		C.uio_dev_destroy(s.fd)
		C.close(s.fd)
		s.fd = -1
	}
}

func (s *UinputServer) Tap(code int) {
	C.uio_tap(s.fd, C.uint16_t(code))
}

func (s *UinputServer) Backspace() {
	s.Tap(C.KEY_BACKSPACE)
}

func (s *UinputServer) Type(text string) {
	// Use ydotool to type text. ydotool uses /dev/uinput directly
	// (evdev), bypassing Wayland compositor protocols entirely.
	cmd := exec.Command("ydotool", "type", "-")
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[uinput] ydotool type failed: %v: %s", err, string(out))
	}
}

func (s *UinputServer) typeCodepoint(cp uint32) {
	hex := fmt.Sprintf("%x", cp)
	// Ctrl+Shift+U compose sequence
	C.uio_key(s.fd, C.KEY_LEFTCTRL, 1)
	C.uio_key(s.fd, C.KEY_LEFTSHIFT, 1)
	s.Tap(C.KEY_U)
	C.uio_key(s.fd, C.KEY_LEFTSHIFT, 0)
	C.uio_key(s.fd, C.KEY_LEFTCTRL, 0)
	C.usleep(2000)
	for _, h := range hex {
		ch := h
		if ch >= '0' && ch <= '9' {
			s.Tap(int(C.KEY_0) + int(ch-'0'))
		} else if ch >= 'a' && ch <= 'f' {
			s.Tap(int(C.KEY_A) + int(ch-'a'))
		}
		C.usleep(500)
	}
	s.Tap(C.KEY_ENTER)
}

func (s *UinputServer) typeUtf8(text string) {
	for _, r := range text {
		s.typeCodepoint(uint32(r))
	}
}

func hexKeyCode(ch rune) int {
	if ch >= '0' && ch <= '9' {
		return int(C.KEY_0) + int(ch-'0')
	}
	if ch >= 'a' && ch <= 'f' {
		return int(C.KEY_A) + int(ch-'a')
	}
	if ch >= 'A' && ch <= 'F' {
		return int(C.KEY_A) + int(ch-'A')
	}
	return -1
}

// Protocol: int32 count + uint32 textLen + text bytes
func (s *UinputServer) handlePacket(data []byte) {
	s.packetMu.Lock()
	defer s.packetMu.Unlock()

	if len(data) < 8 {
		return
	}
	count := int32(binary.LittleEndian.Uint32(data[0:4]))
	textLen := binary.LittleEndian.Uint32(data[4:8])
	var text string
	if textLen > 0 && int(8+textLen) <= len(data) {
		text = string(data[8 : 8+textLen])
	}

	for i := int32(0); i < count; i++ {
		s.Backspace()
		C.usleep(C.useconds_t(1000))
	}
	if text != "" {
		C.usleep(C.useconds_t(3000))
		s.typeUtf8(text)
	}
}

type clientConn struct {
	conn *net.UnixConn
}

func (s *UinputServer) Run(sockPath string) error {
	addr := &net.UnixAddr{Name: "@" + sockPath, Net: "unix"}
	l, err := net.ListenUnix("unix", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer l.Close()

	log.Printf("ktelex-uinput listening on @%s", sockPath)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		log.Printf("shutting down")
		l.Close()
		close(s.done)
	}()

	for {
		conn, err := l.AcceptUnix()
		if err != nil {
			select {
			case <-s.done:
				return nil
			default:
			}
			continue
		}
		go s.handleClient(conn)
	}
}

func (s *UinputServer) handleClient(conn *net.UnixConn) {
	defer conn.Close()
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		s.handlePacket(buf[:n])
	}
}

func main() {
	log.SetPrefix("[uinput] ")
	log.SetFlags(log.Ldate | log.Lmicroseconds)

	srv, err := NewUinputServer()
	if err != nil {
		log.Fatalf("init: %v", err)
	}
	defer srv.Close()

	if err := srv.Run(sockAddr); err != nil {
		log.Fatalf("run: %v", err)
	}
}
