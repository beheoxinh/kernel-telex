package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	wmclassHelperBusName = "org.gnome.Shell.WMClassHelper"
	wmclassHelperObjPath = "/org/gnome/Shell/WMClassHelper"
	wmclassHelperIface   = "org.gnome.Shell.WMClassHelper"
)

// One private session-bus connection is reused for every query. The old code
// dialled (and closed) a connection per call — 2-3 connect+auth+Hello round
// trips on every FocusIn, and Chromium flaps FocusIn many times a second.
// It also closed the *shared* SessionBus() connection used elsewhere.
var (
	gnomeBusMu sync.Mutex
	gnomeBus   *dbus.Conn
)

const gnomeCallTimeout = 250 * time.Millisecond

func gnomeConn() (*dbus.Conn, error) {
	gnomeBusMu.Lock()
	defer gnomeBusMu.Unlock()
	if gnomeBus != nil && gnomeBus.Connected() {
		return gnomeBus, nil
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("session bus: %w", err)
	}
	gnomeBus = conn
	return conn, nil
}

func gnomeCall(dest string, path dbus.ObjectPath, method string, args ...interface{}) *dbus.Call {
	conn, err := gnomeConn()
	if err != nil {
		return &dbus.Call{Err: err}
	}
	ctx, cancel := context.WithTimeout(context.Background(), gnomeCallTimeout)
	defer cancel()
	return conn.Object(dest, path).CallWithContext(ctx, method, 0, args...)
}

func gnomeGetFocusWindowClass() (string, error) {
	if wmClass, err := getWmClassFromExtension(); err == nil && wmClass != "" {
		return wmClass, nil
	}
	return getWmClassFromEval()
}

func getWmClassFromExtension() (string, error) {
	call := gnomeCall(wmclassHelperBusName, dbus.ObjectPath(wmclassHelperObjPath), wmclassHelperIface+".GetFocusedWindowClass")
	if call.Err != nil {
		return "", fmt.Errorf("WMClassHelper D-Bus: %w", call.Err)
	}
	var wmClass string
	if err := call.Store(&wmClass); err != nil {
		return "", fmt.Errorf("WMClassHelper Store: %w", err)
	}
	return wmClass, nil
}

func getWmClassFromEval() (string, error) {
	js_code := "global.get_window_actors().find(window => !Main.overview.visible && window.meta_window.has_focus()).get_meta_window().get_wm_class()"
	var ok bool
	var s string
	err := gnomeCall("org.gnome.Shell", "/org/gnome/Shell", "org.gnome.Shell.Eval", js_code).Store(&ok, &s)
	if err != nil {
		return "", err
	}
	if !ok {
		if isGnomeOverviewVisible() {
			return "org.gnome.Overview", nil
		}
		return "", errors.New(s)
	}
	return s, nil
}

func gnomeGetFocusPID() (int, error) {
	if pid, err := getPIDFromExtension(); err == nil && pid > 0 {
		return pid, nil
	}
	var ok bool
	var pidStr string
	// On GNOME 45+ get_pid() may throw — catch and return an empty string.
	js := `
		try {
			let a = global.get_window_actors().find(w => !Main.overview.visible && w.meta_window.has_focus());
			if (!a) { '' }
			else { String(a.get_meta_window().get_pid()) }
		} catch(e) { '' }
	`
	_ = gnomeCall("org.gnome.Shell", "/org/gnome/Shell", "org.gnome.Shell.Eval", js).Store(&ok, &pidStr)
	if ok {
		pidStr = strings.TrimSpace(pidStr)
		if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
			log.Printf("[gnomeGetFocusPID] %d", pid)
			return pid, nil
		}
	}
	return 0, fmt.Errorf("gnome eval pid failed: ok=%v pid=%q", ok, pidStr)
}

func getPIDFromExtension() (int, error) {
	call := gnomeCall(wmclassHelperBusName, dbus.ObjectPath(wmclassHelperObjPath), wmclassHelperIface+".GetFocusedWindowPID")
	if call.Err != nil {
		return 0, fmt.Errorf("WMClassHelper D-Bus: %w", call.Err)
	}
	var pid int32
	if err := call.Store(&pid); err != nil {
		return 0, fmt.Errorf("WMClassHelper Store: %w", err)
	}
	return int(pid), nil
}

func isGnomeOverviewVisible() bool {
	var visible string
	var ok bool
	err := gnomeCall("org.gnome.Shell", "/org/gnome/Shell", "org.gnome.Shell.Eval", "Main.overview.visible").Store(&ok, &visible)
	if !ok || err != nil {
		return false
	}
	return visible == "true"
}
