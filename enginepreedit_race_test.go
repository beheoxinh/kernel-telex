package main

import (
	"sync"
	"testing"

	"ktelex/config"
)

// Preedit mode: keys, Reset and FocusOut arrive on separate D-Bus goroutines
// and must not race on the composer (run with -race).
func TestPreeditModeConcurrentReset(t *testing.T) {
	e, _ := newUinputEchoEngine(t)
	e.wmClasses = "preedit-app:Preedit-app"
	e.config.InputModeMapping = map[string]int{"preedit-app:Preedit-app": config.PreeditIM}
	e.bumpConfigGen()
	if e.getInputMode() != config.PreeditIM {
		t.Skip("mapping not applied")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			for _, k := range "tieengs " {
				e.ProcessKeyEvent(uint32(k), 0, 0)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			e.Reset()
			e.FocusOut()
		}
	}()
	wg.Wait()
}
