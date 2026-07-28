package main

import (
	"encoding/binary"
	"log"
	"net"
	"sync"
)

var (
	uinputMu   sync.Mutex
	uinputSock = "@ktelex-uinput"
)

func uinputSendRaw(buf []byte) {
	uinputMu.Lock()
	defer uinputMu.Unlock()
	conn, err := net.Dial("unix", uinputSock)
	if err != nil {
		log.Printf("[uinput] dial failed: %v", err)
		return
	}
	defer conn.Close()
	if _, err := conn.Write(buf); err != nil {
		log.Printf("[uinput] write failed: %v", err)
	}
}

// uinputBackspace sends n backspace keystrokes via uinput.
// Uses direct fd if available (in-process), otherwise falls back to socket.
func uinputBackspace(n int) {
	if uinputFd >= 0 {
		uinputDirectBackspace(n)
		return
	}
	uinputReplace(n, "")
}

// uinputReplace sends backspace count + replacement text in one packet
// to the standalone uinput server via Unix socket.
func uinputReplace(count int, text string) {
	textBytes := []byte(text)
	buf := make([]byte, 8+len(textBytes))
	binary.LittleEndian.PutUint32(buf[0:4], uint32(count))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(textBytes)))
	copy(buf[8:], textBytes)
	uinputSendRaw(buf)
}

func uinputTestConn() bool {
	conn, err := net.Dial("unix", uinputSock)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func uinputInit() {
	go func() {
		for i := 0; i < 30; i++ {
			if uinputTestConn() {
				log.Printf("[uinput] connected to server")
				return
			}
		}
		log.Printf("[uinput] server not available after 6s")
	}()
}
