package main

import (
	"fmt"
	"log"
	"sync/atomic"

	wl "github.com/dkolbly/wl"
)

// wlFocusedAppId holds the app-id of the activated toplevel, as reported by
// wlroots compositors (sway, Hyprland, …) through the foreign-toplevel
// protocol. GNOME does not offer that protocol: its focus comes from the
// shell (gnome_introspector.go).
var wlFocusedAppId atomic.Value // string

func wlAppId() string {
	s, _ := wlFocusedAppId.Load().(string)
	return s
}

// wlTrackFocus keeps one connection to the compositor and follows the
// activated toplevel for the life of the engine. It returns when the
// compositor does not offer the protocol.
func wlTrackFocus() {
	display, err := wl.Connect("")
	if err != nil {
		log.Printf("wayland focus: %v", err)
		return
	}
	appIdChan := make(chan string, 1)
	if err := registerGlobals(display, appIdChan); err != nil {
		log.Printf("wayland focus: %v", err)
		display.Context().Close()
		return
	}
	if !wlManagerBound.Load() {
		log.Printf("wayland focus: compositor has no zwlr_foreign_toplevel_manager_v1")
		display.Context().Close()
		return
	}
	go func() {
		for id := range appIdChan {
			wlFocusedAppId.Store(id)
		}
	}()
	// Each token lets the context read and dispatch one event. When the
	// compositor goes away the context stops reading and this blocks: one
	// idle goroutine, the last app-id stays.
	for {
		display.Context().Dispatch() <- struct{}{}
	}
}

var wlManagerBound atomic.Bool

func registerGlobals(display *wl.Display, appIdChan chan string) error {
	registry, err := display.GetRegistry()
	if err != nil {
		return fmt.Errorf("Display.GetRegistry failed : %s", err)
	}

	callback, err := display.Sync()
	if err != nil {
		return fmt.Errorf("Display.Sync failed %s", err)
	}

	rgeChan := make(chan wl.RegistryGlobalEvent)
	rgeHandler := registrar{rgeChan}
	registry.AddGlobalHandler(rgeHandler)

	cdeChan := make(chan wl.CallbackDoneEvent)
	cdeHandler := doner{cdeChan}

	callback.AddDoneHandler(cdeHandler)
	adHandler := appIdHandler{appIdChan}
loop:
	for {
		select {
		case ev := <-rgeChan:
			if err := registerInterface(registry, ev, display.Context(), &adHandler); err != nil {
				return err
			}
		case display.Context().Dispatch() <- struct{}{}:
		case <-cdeChan:
			break loop
		}
	}

	registry.RemoveGlobalHandler(rgeHandler)
	callback.RemoveDoneHandler(cdeHandler)
	return nil
}

func registerInterface(registry *wl.Registry, ev wl.RegistryGlobalEvent, ctx *wl.Context, adHandler *appIdHandler) error {
	switch ev.Interface {
	case "zwlr_foreign_toplevel_manager_v1":
		manager := NewZwlrForeignToplevelManagerV1(ctx)
		manager.AddToplevelHandler(toplevelHandlers{adHandler})
		err := registry.Bind(ev.Name, ev.Interface, ev.Version, manager)
		if err != nil {
			return fmt.Errorf("Unable to bind ZwlrForeignToplevelManagerV1 interface: %s", err)
		}
		wlManagerBound.Store(true)
	}
	return nil
}

type doner struct {
	ch chan wl.CallbackDoneEvent
}

func (d doner) HandleCallbackDone(ev wl.CallbackDoneEvent) {
	d.ch <- ev
}

type registrar struct {
	ch chan wl.RegistryGlobalEvent
}

func (r registrar) HandleRegistryGlobal(ev wl.RegistryGlobalEvent) {
	r.ch <- ev
}

type toplevelHandlers struct {
	adHandler *appIdHandler
}

func (t toplevelHandlers) HandleZwlrForeignToplevelManagerV1Toplevel(ev ZwlrForeignToplevelManagerV1ToplevelEvent) {
	h := &toplevelState{ch: t.adHandler.ch}
	ev.Toplevel.AddAppIdHandler(h)
	ev.Toplevel.AddStateHandler(h)
	ev.Toplevel.AddDoneHandler(h)
}

// appIdHandler carries the channel the focused app-id is sent on.
type appIdHandler struct {
	ch chan string
}

// toplevelState collects one toplevel's app-id and state; "done" applies
// them atomically. The activated toplevel is the focused window.
type toplevelState struct {
	ch        chan string
	appId     string
	activated bool
}

func (t *toplevelState) HandleZwlrForeignToplevelHandleV1AppId(ev ZwlrForeignToplevelHandleV1AppIdEvent) {
	t.appId = ev.AppId
}

func (t *toplevelState) HandleZwlrForeignToplevelHandleV1State(ev ZwlrForeignToplevelHandleV1StateEvent) {
	t.activated = false
	for _, st := range ev.State {
		if st == ZwlrForeignToplevelHandleV1StateActivated {
			t.activated = true
		}
	}
}

func (t *toplevelState) HandleZwlrForeignToplevelHandleV1Done(ev ZwlrForeignToplevelHandleV1DoneEvent) {
	if !t.activated {
		return
	}
	// Latest wins: drop an unread older value instead of blocking the
	// dispatch goroutine.
	select {
	case t.ch <- t.appId:
	default:
		select {
		case <-t.ch:
		default:
		}
		select {
		case t.ch <- t.appId:
		default:
		}
	}
}
