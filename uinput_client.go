package main

// uinputBackspaceHook replaces the injection in tests.
var uinputBackspaceHook func(n int) bool

// uinputBackspace sends n backspace keystrokes through the in-process
// /dev/uinput device. When the device is not open yet it wakes the retry
// loop and returns false, so the caller falls back to non-uinput paths.
func uinputBackspace(n int) bool {
	if uinputBackspaceHook != nil {
		return uinputBackspaceHook(n)
	}
	if uinputDirectBackspace(n) {
		return true
	}
	uinputRequestRetry()
	return false
}
