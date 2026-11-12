/*
 * Bamboo - A Vietnamese Input method editor
 * Copyright (C) 2018 Luong Thanh Lam <ltlam93@gmail.com>
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package main

import (
	"bufio"
	"ktelex/config"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// MacroTable holds the abbreviations (gõ tắt). The key path reads it while
// a single background poller reloads the file when it changes: a reload
// builds a new map and swaps it in under the lock, so a reader never sees
// a map being written (which crashed the whole engine process).
type MacroTable struct {
	mu                  sync.RWMutex
	autoCapitalizeMacro bool
	mTable              map[string]string
	stop                chan struct{} // non-nil while the poller runs
}

const kMacroPollInterval = 3 * time.Second

func NewMacroTable(autoCapitalizeMacro bool) *MacroTable {
	return &MacroTable{autoCapitalizeMacro: autoCapitalizeMacro}
}

// parseMacros reads "key:text" lines; the text may itself contain ':'.
func parseMacros(f *os.File, lowerKeys bool) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, ";") || strings.HasPrefix(s, "#") {
			continue
		}
		key, text, ok := strings.Cut(s, ":")
		if !ok {
			continue
		}
		key, text = strings.TrimSpace(key), strings.TrimSpace(text)
		if key == "" || text == "" {
			continue
		}
		if lowerKeys {
			key = strings.ToLower(key)
		}
		m[key] = text
	}
	return m
}

func (e *MacroTable) LoadFromFile(macroFileName string) error {
	f, err := os.Open(macroFileName)
	if err != nil {
		return err
	}
	defer f.Close()
	e.mu.RLock()
	lower := e.autoCapitalizeMacro
	e.mu.RUnlock()
	m := parseMacros(f, lower)
	e.mu.Lock()
	e.mTable = m
	e.mu.Unlock()
	return nil
}

// AutoCapitalize reports whether keys match case-insensitively.
func (e *MacroTable) AutoCapitalize() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.autoCapitalizeMacro
}

// SetAutoCapitalize changes the key matching and reloads the file (keys
// are stored lower-cased when it is on).
func (e *MacroTable) SetAutoCapitalize(engineName string, on bool) {
	e.mu.Lock()
	changed := e.autoCapitalizeMacro != on
	e.autoCapitalizeMacro = on
	e.mu.Unlock()
	if changed {
		e.LoadFromFile(config.GetMacroPath(engineName))
	}
}

func (e *MacroTable) Reload(engineName string, autoCapitalizeMacro bool) {
	e.mu.Lock()
	e.autoCapitalizeMacro = autoCapitalizeMacro
	running := e.stop != nil
	e.mu.Unlock()
	if running {
		e.LoadFromFile(config.GetMacroPath(engineName))
		return
	}
	e.Enable(engineName)
}

func (e *MacroTable) key(key string) string {
	if e.autoCapitalizeMacro {
		return strings.ToLower(key)
	}
	return key
}

func (e *MacroTable) GetText(key string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.mTable[e.key(key)]
}

func (e *MacroTable) HasKey(key string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.mTable[e.key(key)] != ""
}

func (e *MacroTable) HasPrefix(key string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	key = e.key(key)
	if e.mTable[key] != "" {
		return true
	}
	for k := range e.mTable {
		if strings.HasPrefix(k, key) {
			return true
		}
	}
	return false
}

// Enable loads the macro file and starts the (single) change poller.
func (e *MacroTable) Enable(engineName string) {
	e.mu.Lock()
	if e.stop != nil {
		e.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	e.stop = stop
	e.mu.Unlock()

	path := config.GetMacroPath(engineName)
	go func() {
		var modTime time.Time
		t := time.NewTicker(kMacroPollInterval)
		defer t.Stop()
		for {
			if st, err := os.Stat(path); err == nil && !st.ModTime().Equal(modTime) {
				modTime = st.ModTime()
				if err := e.LoadFromFile(path); err != nil {
					log.Printf("macro: load %s: %v", path, err)
				}
			}
			select {
			case <-stop:
				return
			case <-t.C:
			}
		}
	}()
}

func (e *MacroTable) Disable() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stop != nil {
		close(e.stop)
		e.stop = nil
	}
	e.mTable = map[string]string{}
}
