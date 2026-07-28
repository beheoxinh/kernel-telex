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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/BambooEngine/bamboo-core"
)

const (
	VnCaseAllSmall uint8 = iota + 1
	VnCaseAllCapital
	VnCaseNoChange
)
const (
	HomePage          = "https://github.com/BambooEngine/ibus-bamboo"

	DataDir          = "/usr/share/ktelex"
	DictVietnameseCm = "data/vietnamese.cm.dict"
	DictEmojiOne     = "data/emojione.json"
)

const (
	configDir        = "%s/.config/ibus-%s"
	configFile       = "%s/ibus-%s.config.json"
	mactabFile       = "%s/ibus-%s.macro.text"
	sampleMactabFile = "data/macro.tpl.txt"
)

// Keyboard Shortcuts with keyVal-mask position
const (
	KSInputModeSwitch uint = iota * 2
	KSRestoreKeyStrokes
	KSViEnSwitch
	KSEmojiDialog
)

var enabledAuxiliaryTextList = []string{
	"wpsoffice:wpsoffice",
}

var DefaultBrowserList = []string{
	"Navigator:Firefox",
	"google-chrome:Google-chrome",
	"chromium-browser:Chromium-browser",
}

func getEngineSubFile(fileName string) string {
	if _, err := os.Stat(fileName); err == nil {
		// return source code data/macro.tpl.txt path
		if absPath, err := filepath.Abs(fileName); err == nil {
			return absPath
		}
	}

	// return installation data/macro.tpl.txt path
	fileName = "../../share/ktelex/" + fileName
	return filepath.Join(filepath.Dir(os.Args[0]), fileName)
}

func determineMacroCase(str string) uint8 {
	var chars = []rune(str)
	if unicode.IsLower(chars[0]) {
		return VnCaseAllSmall
	} else {
		for _, c := range chars[1:] {
			if unicode.IsLower(c) {
				return VnCaseNoChange
			}
			if bamboo.IsWordBreakSymbol(c) {
				return VnCaseNoChange
			}
		}
	}
	return VnCaseAllCapital
}

func inKeyList(list []rune, key rune) bool {
	for _, s := range list {
		if s == key {
			return true
		}
	}
	return false
}

func inStringList(list []string, str string) bool {
	for _, s := range list {
		if s == str {
			return true
		}
	}
	return false
}

func removeFromWhiteList(list []string, classes string) []string {
	var newList []string
	for _, cl := range list {
		if cl != classes {
			newList = append(newList, cl)
		}
	}
	return newList
}

func addToWhiteList(list []string, classes string) []string {
	for _, cl := range list {
		if cl == classes {
			return list
		}
	}
	return append(list, classes)
}

func getValueFromPropKey(str, key string) (string, bool) {
	var arr = strings.Split(str, "::")
	if len(arr) == 2 && arr[0] == key {
		return arr[1], true
	}
	return str, false
}

func isValidCharset(str string) bool {
	var charsets = bamboo.GetCharsetNames()
	for _, cs := range charsets {
		if cs == str {
			return true
		}
	}
	return false
}

type byString []string

func (s byString) Less(i, j int) bool {
	return s[i] < s[j]
}
func (s byString) Len() int {
	return len(s)
}
func (s byString) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func sortStrings(list []string) []string {
	var strList = byString(list)
	sort.Sort(strList)
	return strList
}

func loadDictionary(dataFiles ...string) (map[string]bool, error) {
	var data = map[string]bool{}
	for _, dataFile := range dataFiles {
		f, err := os.Open(dataFile)
		if err != nil {
			return nil, err
		}
		rd := bufio.NewReader(f)
		for {
			line, _, err := rd.ReadLine()
			if err != nil {
				break
			}
			if len(line) == 0 {
				continue
			}
			var tmp = []byte(strings.ToLower(string(line)))
			data[string(tmp)] = true
			//bamboo.AddTrie(rootWordTrie, []rune(string(line)), false)
		}
		f.Close()
	}
	return data, nil
}

func isMovementKey(keyVal uint32) bool {
	var list = []uint32{IBusLeft, IBusRight, IBusUp, IBusDown, IBusPageDown, IBusPageUp, IBusEnd}
	for _, item := range list {
		if item == keyVal {
			return true
		}
	}
	return false
}


