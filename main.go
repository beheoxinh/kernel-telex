package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"ktelex/ui"
	ibus "github.com/BambooEngine/goibus"
)

const (
	ComponentName = "org.freedesktop.IBus.ktelex"
	EngineName    = "Ktelex"
)

var embedded = flag.Bool("ibus", false, "Run the embedded ibus component")
var version = flag.Bool("version", false, "Show version")
var gui = flag.Bool("gui", false, "Show GUI")
var isWayland = false
var isGnome = false

func hasGnome(env string) bool {
	return strings.Contains(strings.ToLower(os.Getenv(env)), "gnome")
}

func main() {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		isWayland = true
	}
	if hasGnome("XDG_CURRENT_DESKTOP") || hasGnome("DESKTOP_SESSION") || hasGnome("GDMSESSION") {
		isGnome = true
	}
	flag.Parse()
	if *embedded {
		os.Chdir(DataDir)
		// Redirect log output to file for debugging
		f, err := os.OpenFile("/tmp/ktelex.log", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err == nil {
			log.SetOutput(f)
		}
	}
	if isWayland && !isGnome {
		go wlGetFocusWindowClass()
	}
	if *version {
		fmt.Println(Version)
	} else if *gui {
		ui.OpenGUI("ktelex", Version, BuildId)
		os.Exit(0)
	} else if *embedded {
		go uinputInitDirect()
		engine := GetIBusEngineCreator()
		bus := ibus.NewBus()
		bus.RequestName(ComponentName, 0)

		conn := bus.GetDbusConn()
		ibus.NewFactory(conn, engine)

		select {}
	}
}
