package main

import (
	"os"
	"testing"
)

// Tests never touch the real /dev/uinput: injected Backspaces "succeed" and
// their echoes are fed by the tests themselves.
func TestMain(m *testing.M) {
	uinputBackspaceHook = func(int) bool { return true }
	unstickOff = true
	// Config files go to a throwaway HOME, never the user's real one.
	home, err := os.MkdirTemp("", "ktelex-test-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
