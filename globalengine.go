package main

// Login-time engine desync heal.
//
// GNOME owns the input-source selection and tells ibus-daemon which engine
// to drive. Right after login/relog (or an ibus restart) that hand-off can
// race: GNOME shows "VI" (its active source, mru-sources[0], is KTelex) but
// ibus-daemon ends up driving KtelexUs — e.g. because KtelexUs is first in
// org.gnome.desktop.input-sources "sources". Every key then goes through the
// English passthrough engine, so nothing is converted until the user
// switches EN -> VI by hand, which makes GNOME set the engine again.
//
// Fix: when one of our English engines becomes active shortly after the
// process started, re-read GNOME's active source; if GNOME says KTelex, ask
// ibus-daemon to switch the global engine to KTelex. A deliberate switch to
// English updates mru-sources first, so it is never reverted.

import (
	"context"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	ibus "github.com/BambooEngine/goibus"
	"github.com/godbus/dbus/v5"
)

const (
	viEngineID       = "KTelex"
	kHealWindow      = 90 * time.Second       // only right after the engine process started
	kHealDelay       = 400 * time.Millisecond // let GNOME finish writing its settings first
	kHealMaxAttempts = 3
)

var (
	processStartAt = time.Now()
	ibusConn       *dbus.Conn // IBus connection, set by main once connected

	healMu           sync.Mutex
	activeEngineName string
	healPending      bool
	healAttempts     int

	// Test hooks.
	gnomeActiveSourceHook func() (string, bool)
	setGlobalEngineHook   func(id string) error
)

func isEnglishEngine(name string) bool {
	return name == "ktelexus" || name == "ktelexgb"
}

// noteEngineActive records which of our engines ibus-daemon is driving
// (called on Enable and FocusIn) and schedules a desync check when an
// English engine shows up during the start-up window.
func noteEngineActive(name string) {
	healMu.Lock()
	activeEngineName = name
	if !isEnglishEngine(name) || healPending || healAttempts >= kHealMaxAttempts ||
		time.Since(processStartAt) > kHealWindow {
		healMu.Unlock()
		return
	}
	healPending = true
	healMu.Unlock()
	time.AfterFunc(kHealDelay, healGlobalEngine)
}

func healGlobalEngine() {
	healMu.Lock()
	healPending = false
	cur := activeEngineName
	healMu.Unlock()
	if !isEnglishEngine(cur) {
		return // the Vietnamese engine took over meanwhile
	}
	src, ok := gnomeActiveSource()
	if !ok || !strings.EqualFold(src, viEngineID) {
		return // GNOME really selected English (or unknown desktop)
	}
	healMu.Lock()
	healAttempts++
	n := healAttempts
	healMu.Unlock()
	log.Printf("[engine-heal] ibus drives %q but GNOME selected %q: switching global engine (attempt %d)", cur, src, n)
	if err := setGlobalEngine(viEngineID); err != nil {
		log.Printf("[engine-heal] SetGlobalEngine failed: %v", err)
	}
}

var mruTupleRe = regexp.MustCompile(`\(\s*'([^']*)'\s*,\s*'([^']*)'\s*\)`)

// parseFirstIbusSource returns the engine id of the first ('ibus', id) tuple
// in a gsettings a(ss) value; ok=false if the first source is not ibus.
func parseFirstIbusSource(v string) (string, bool) {
	m := mruTupleRe.FindStringSubmatch(v)
	if m == nil || m[1] != "ibus" {
		return "", false
	}
	return m[2], true
}

// gnomeActiveSource returns the ibus engine id of GNOME's active input
// source (mru-sources[0]).
func gnomeActiveSource() (string, bool) {
	healMu.Lock()
	hook := gnomeActiveSourceHook
	healMu.Unlock()
	if hook != nil {
		return hook()
	}
	if !isGnome {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gsettings", "get", "org.gnome.desktop.input-sources", "mru-sources").Output()
	if err != nil {
		return "", false
	}
	return parseFirstIbusSource(string(out))
}

func setGlobalEngine(id string) error {
	healMu.Lock()
	hook := setGlobalEngineHook
	healMu.Unlock()
	if hook != nil {
		return hook(id)
	}
	if ibusConn == nil {
		return errNoBus
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	obj := ibusConn.Object(ibus.IBUS_SERVICE_IBUS, dbus.ObjectPath(ibus.IBUS_PATH_IBUS))
	return obj.CallWithContext(ctx, ibus.IBUS_SERVICE_IBUS+".SetGlobalEngine", 0, id).Err
}

type healError string

func (e healError) Error() string { return string(e) }

const errNoBus = healError("ibus bus not connected")
