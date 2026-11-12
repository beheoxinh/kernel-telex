package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func setTempHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestWriteFileAtomicLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "f.json")
	for _, v := range []string{"one", "two"} {
		if err := WriteFileAtomic(p, []byte(v), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Fatalf("content %q", b)
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Fatalf("leftover files: %v", ents)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0644 {
		t.Fatalf("perm %v", st.Mode().Perm())
	}
}

func TestBrokenConfigIsBackedUp(t *testing.T) {
	setTempHome(t)
	p := GetConfigPath("ktelex")
	broken := []byte(`{"InputMethod": "VNI",`)
	if err := WriteFileAtomic(p, broken, 0644); err != nil {
		t.Fatal(err)
	}
	c := LoadConfig("ktelex")
	if c.InputMethod != "Telex" {
		t.Fatalf("expected defaults, got %q", c.InputMethod)
	}
	if b, _ := os.ReadFile(p + ".broken"); string(b) != string(broken) {
		t.Fatalf("backup = %q", b)
	}
	if b, _ := os.ReadFile(p); string(b) != string(broken) {
		t.Fatalf("loading must not rewrite the broken file")
	}
}

func TestENUsesMasterMappingAndNeverSavesIt(t *testing.T) {
	setTempHome(t)
	if err := UpdateConfig("ktelex", func(c *Config) { c.InputModeMapping["foo:Foo"] = UsIM }); err != nil {
		t.Fatal(err)
	}
	// Leftover of the old bug: a merged copy in the EN file.
	if err := UpdateConfig("ktelexus", func(c *Config) { c.InputModeMapping["stale:Stale"] = PreeditIM }); err != nil {
		t.Fatal(err)
	}
	c := LoadConfig("ktelexus")
	if c.InputModeMapping["foo:Foo"] != UsIM {
		t.Fatalf("master mapping missing: %v", c.InputModeMapping)
	}
	if _, ok := c.InputModeMapping["stale:Stale"]; ok {
		t.Fatalf("EN file mapping must be ignored")
	}
	if err := UpdateConfig("ktelexus", func(c *Config) { c.OutputCharset = "TCVN3" }); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(GetConfigPath("ktelexus"))
	if strings.Contains(string(b), "foo:Foo") {
		t.Fatalf("EN file got the master mapping:\n%s", b)
	}
}

func TestUpdateConfigConcurrentNoLostUpdate(t *testing.T) {
	setTempHome(t)
	var wg sync.WaitGroup
	const n = 20
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			UpdateConfig("ktelex", func(c *Config) { c.InputModeMapping[fmt.Sprintf("app-%d", i)] = PreeditIM })
		}(i)
	}
	wg.Wait()
	c := LoadConfig("ktelex")
	for i := 0; i < n; i++ {
		if _, ok := c.InputModeMapping[fmt.Sprintf("app-%d", i)]; !ok {
			t.Fatalf("lost update app-%d", i)
		}
	}
}
