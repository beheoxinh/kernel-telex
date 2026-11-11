package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	ibus "github.com/BambooEngine/goibus"
	"github.com/godbus/dbus/v5"
	"ktelex/ui"
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

// connectIBus is ibus.NewBus with the key-ordering interceptor installed
// (see keyorder.go), and errors returned instead of panicking.
func connectIBus() (*dbus.Conn, error) {
	conn, err := dbus.Dial(ibus.GetAddress(), dbus.WithIncomingInterceptor(keyOrderInterceptor))
	if err != nil {
		return nil, err
	}
	if err = conn.Auth(ibus.GetUserAuth()); err != nil {
		conn.Close()
		return nil, err
	}
	if err = conn.Hello(); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
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
		f, err := openLogFile("/tmp/ktelex.log")
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
		conn, err := connectIBus()
		if err != nil {
			log.Fatalf("connect ibus: %v", err)
		}
		ibusConn = conn
		// Export the factory BEFORE owning the name: ibus-daemon calls
		// CreateEngine as soon as the name appears, and an unexported
		// factory then fails engine creation at login.
		ibus.NewFactory(conn, engine)
		conn.RequestName(ComponentName, 0)

		select {}
	}
}
