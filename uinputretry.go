package main

import (
	"log"
	"time"
)

// Background retry of the in-process uinput device. Kernel mode cannot
// delete anything without it (unless the standalone socket server runs), so
// a failed open at startup must not be final.

const (
	kUinputRetryBase   = time.Second
	kUinputRetryMax    = 60 * time.Second
	kUinputNotifyAfter = 3 // failed attempts (~7s) before telling the user
)

// uinputKick wakes the retry loop early, e.g. when a Backspace is needed.
var uinputKick = make(chan struct{}, 1)

func uinputRequestRetry() {
	select {
	case uinputKick <- struct{}{}:
	default:
	}
}

// uinputOpenLoop calls open until it succeeds, with exponential backoff
// (base doubling up to max); kick triggers an attempt right away. notice is
// called once, after kUinputNotifyAfter failures.
func uinputOpenLoop(open func() bool, notice func(), kick <-chan struct{}, base, max time.Duration, stop <-chan struct{}) {
	delay := base
	for attempt := 1; ; attempt++ {
		select {
		case <-time.After(delay):
		case <-kick:
		case <-stop:
			return
		}
		if open() {
			log.Printf("[uinput] device available after %d retries", attempt)
			return
		}
		if attempt == kUinputNotifyAfter && notice != nil {
			notice()
		}
		if delay *= 2; delay > max {
			delay = max
		}
	}
}

func uinputNotifyUnavailable() {
	if uinputTestConn() {
		return // the socket server does the injection
	}
	log.Printf("[uinput] /dev/uinput still unavailable: Kernel mode cannot delete characters")
	notifyDesktop("KTelex: chế độ Kernel chưa sẵn sàng",
		"Không mở được /dev/uinput nên chưa xoá được ký tự khi bỏ dấu. "+
			"Kiểm tra quyền thiết bị (udev rule / nhóm input); KTelex sẽ tự thử lại.")
}
