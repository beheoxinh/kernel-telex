package main

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/godbus/dbus/v5"
)

const (
	wmclassHelperBusName = "org.gnome.Shell.WMClassHelper"
	wmclassHelperObjPath = "/org/gnome/Shell/WMClassHelper"
	wmclassHelperIface   = "org.gnome.Shell.WMClassHelper"
)

func gnomeGetFocusWindowClass() (string, error) {
	if wmClass, err := getWmClassFromExtension(); err == nil && wmClass != "" {
		return wmClass, nil
	}
	return getWmClassFromEval()
}

func getWmClassFromExtension() (string, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return "", fmt.Errorf("session bus: %w", err)
	}
	defer conn.Close()

	obj := conn.Object(wmclassHelperBusName, dbus.ObjectPath(wmclassHelperObjPath))
	call := obj.Call(wmclassHelperIface+".GetFocusedWindowClass", 0)
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
	conn, err := dbus.SessionBus()
	var s string
	if err != nil {
		return s, err
	}
	defer func() {
		if err = conn.Hello(); err == nil {
			conn.Close()
		}
	}()

	js_code := "global.get_window_actors().find(window => !Main.overview.visible && window.meta_window.has_focus()).get_meta_window().get_wm_class()"
	obj := conn.Object("org.gnome.Shell", "/org/gnome/Shell")
	var ok bool
	err = obj.Call("org.gnome.Shell.Eval", 0, js_code).Store(&ok, &s)
	if !ok {
		if isGnomeOverviewVisible(conn) {
			return "org.gnome.Overview", nil
		} else {
			err = errors.New(s)
		}
	}
	if err != nil {
		return "", err
	}
	return s, nil
}

func gnomeGetFocusPID() (int, error) {
	if pid, err := getPIDFromExtension(); err == nil && pid > 0 {
		return pid, nil
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return 0, fmt.Errorf("session bus: %w", err)
	}
	defer func() {
		if err = conn.Hello(); err == nil {
			conn.Close()
		}
	}()

	obj := conn.Object("org.gnome.Shell", "/org/gnome/Shell")
	var ok bool
	var pidStr string

	// Use the same window-actor pattern as getWmClassFromEval (proven to work),
	// but also get pid.  On GNOME 45+ get_pid() may throw if the method doesn't
	// exist on the current MetaWindow — catch that and return empty string.
	js := `
		try {
			let a = global.get_window_actors().find(w => !Main.overview.visible && w.meta_window.has_focus());
			if (!a) { '' }
			else { String(a.get_meta_window().get_pid()) }
		} catch(e) { '' }
	`
	err = obj.Call("org.gnome.Shell.Eval", 0, js).Store(&ok, &pidStr)
	if ok {
		pidStr = strings.TrimSpace(pidStr)
		if pidStr != "" {
			pid, err := strconv.Atoi(pidStr)
			if err == nil && pid > 0 {
				log.Printf("[gnomeGetFocusPID] %d", pid)
				return pid, nil
			}
		}
	}
	return 0, fmt.Errorf("gnome eval pid failed: ok=%v pid=%q", ok, pidStr)
}

func getPIDFromExtension() (int, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return 0, fmt.Errorf("session bus: %w", err)
	}
	defer conn.Close()

	obj := conn.Object(wmclassHelperBusName, dbus.ObjectPath(wmclassHelperObjPath))
	call := obj.Call(wmclassHelperIface+".GetFocusedWindowPID", 0)
	if call.Err != nil {
		return 0, fmt.Errorf("WMClassHelper D-Bus: %w", call.Err)
	}

	var pid int32
	if err := call.Store(&pid); err != nil {
		return 0, fmt.Errorf("WMClassHelper Store: %w", err)
	}
	return int(pid), nil
}

func isGnomeOverviewVisible(conn *dbus.Conn) bool {
	js_code := "Main.overview.visible"
	obj := conn.Object("org.gnome.Shell", "/org/gnome/Shell")
	var visible string
	var ok bool
	err := obj.Call("org.gnome.Shell.Eval", 0, js_code).Store(&ok, &visible)
	if !ok || err != nil {
		return false
	}
	return visible == "true"
}
