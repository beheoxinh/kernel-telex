package main

import (
	"bufio"
	"strings"
	"testing"
	"time"
)

const procDevicesSample = `I: Bus=0003 Vendor=24ae Product=4100 Version=0111
N: Name="Rapoo Rapoo Gaming Device Keyboard"
H: Handlers=event4 mouse0
B: EV=17
B: KEY=1f0000 0 0 0 0

I: Bus=0003 Vendor=24ae Product=4100 Version=0111
N: Name="Rapoo Rapoo Gaming Device"
H: Handlers=sysrq kbd leds event5
B: KEY=3f00733fff 0 0 483ffff17aff32d bfd4444600000000 1 130ff38b17d007 ffff7bfad941dfff ffbeffdfffefffff fffffffffffffffe

I: Bus=0018 Vendor=06cb Product=ce26 Version=0100
N: Name="VEN_06CB:00 06CB:CE26 Touchpad"
H: Handlers=event7 mouse2
B: KEY=e520 10000 0 0 0 0

I: Bus=0003 Vendor=4242 Product=0001 Version=0000
N: Name="Ktelex-Uinput"
H: Handlers=event30 mouse9
B: KEY=1f0000 0 0 0 0
`

func TestPointerEventNodes(t *testing.T) {
	got := pointerEventNodes(bufio.NewScanner(strings.NewReader(procDevicesSample)))
	want := []string{"/dev/input/event4", "/dev/input/event7"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v (keyboard without BTN_LEFT and our own device excluded)", got, want)
	}
}

func withUnstickHooks(t *testing.T, phys bool) (nudges *int, now *time.Time) {
	t.Helper()
	n := 0
	clock := time.Unix(1_800_000_000, 0)
	sNudge, sPhys, sNow := unstickNudge, unstickPhysBS, unstickTimeNow
	unstickNudge = func() bool { n++; return true }
	unstickPhysBS = func() bool { return phys }
	unstickTimeNow = func() time.Time { return clock }
	unstickRiskAt.Store(0)
	unstickLastAt.Store(0)
	t.Cleanup(func() {
		unstickNudge, unstickPhysBS, unstickTimeNow = sNudge, sPhys, sNow
		unstickRiskAt.Store(0)
		unstickLastAt.Store(0)
	})
	return &n, &clock
}

func TestUnstickClickNudgesOnlyAfterRiskyKeys(t *testing.T) {
	nudges, now := withUnstickHooks(t, false)
	if unstickOnPointerPress() || *nudges != 0 {
		t.Fatalf("click without Backspace/arrow activity nudged")
	}
	unstickNoteRisk()
	*now = now.Add(2 * time.Second)
	if !unstickOnPointerPress() || *nudges != 1 {
		t.Fatalf("click after Backspace did not nudge")
	}
	*now = now.Add(time.Second)
	if unstickOnPointerPress() || *nudges != 1 {
		t.Fatalf("second click of the same episode nudged again")
	}
	unstickNoteRisk()
	*now = now.Add(kUnstickRiskWindow + time.Second)
	if unstickOnPointerPress() {
		t.Fatalf("click long after the key activity nudged")
	}
}

func TestUnstickSkipsWhileBackspaceHeld(t *testing.T) {
	nudges, _ := withUnstickHooks(t, true)
	unstickNoteRisk()
	if unstickOnPointerPress() || *nudges != 0 {
		t.Fatalf("nudged while the physical Backspace is down")
	}
}

// The KEY_UNKNOWN tap (no keysym) passes through without touching the
// syllable; a Backspace press marks the risk window.
func TestNoSymbolKeyPassesThrough(t *testing.T) {
	withUnstickHooks(t, false)
	a := newAppModel(t)
	a.typeKey('t')
	a.typeKey('i')
	if ret, _ := a.e.ProcessKeyEvent(0, 240, 0); ret {
		t.Fatalf("no-keysym key consumed")
	}
	if got := a.e.uinputPreeditString(); got != "ti" {
		t.Fatalf("syllable changed to %q", got)
	}
	if unstickRiskAt.Load() != 0 {
		t.Fatalf("risk set without a risky key")
	}
	a.e.ProcessKeyEvent(IBusBackSpace, 14, 0)
	if unstickRiskAt.Load() == 0 {
		t.Fatalf("Backspace press did not open the risk window")
	}
}
