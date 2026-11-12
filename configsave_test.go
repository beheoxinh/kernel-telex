package main

import (
	"os"
	"strings"
	"testing"

	"ktelex/config"

	"github.com/BambooEngine/bamboo-core"
	ibus "github.com/BambooEngine/goibus"
)

// A menu toggle in the engine must not wipe what the settings GUI saved
// after the engine last loaded its config.
func TestPropertyActivateKeepsExternalChanges(t *testing.T) {
	e := newConfigTestEngine(t)
	e.config.Flags &^= bamboo.EstdToneStyle
	config.UpdateConfig(e.engineName, func(c *config.Config) {
		c.OutputCharset = "TCVN3"
		c.InputModeMapping["gui:Gui"] = config.PreeditIM
		c.Flags &^= bamboo.EstdToneStyle
	})
	e.PropertyActivate(PropKeyStdToneStyle, ibus.PROP_STATE_CHECKED)

	c := config.LoadConfig(e.engineName)
	if c.OutputCharset != "TCVN3" || c.InputModeMapping["gui:Gui"] != config.PreeditIM {
		t.Fatalf("external change lost: charset=%q map=%v", c.OutputCharset, c.InputModeMapping)
	}
	if c.Flags&bamboo.EstdToneStyle == 0 {
		t.Fatalf("toggled flag not saved")
	}
	// The newer file is picked up on the next FocusIn.
	if !e.reloadConfigIfChanged() || e.config.OutputCharset != "TCVN3" {
		t.Fatalf("pending external change not reloaded")
	}
}

func TestENPropertyActivateDoesNotCopyMasterMapping(t *testing.T) {
	e, _ := newUinputEchoEngine(t)
	e.engineName = "ktelexus"
	t.Cleanup(func() {
		os.Remove(config.GetConfigPath("ktelexus"))
		os.Remove(config.GetConfigPath("ktelex"))
	})
	config.UpdateConfig("ktelex", func(c *config.Config) { c.InputModeMapping["vi-only:App"] = config.UsIM })
	e.config = config.LoadConfig("ktelexus")
	e.PropertyActivate(PropKeyStdToneStyle, ibus.PROP_STATE_CHECKED)

	b, err := os.ReadFile(config.GetConfigPath("ktelexus"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "vi-only:App") {
		t.Fatalf("EN config got the VI mapping:\n%s", b)
	}
}
