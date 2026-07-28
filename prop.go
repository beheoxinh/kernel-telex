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
	"ktelex/config"

	"github.com/BambooEngine/bamboo-core"
	ibus "github.com/BambooEngine/goibus"
	"github.com/godbus/dbus/v5"
)

const (
	PropKeyAbout                        = "about"
	PropKeyStdToneStyle                 = "std_tone_style"
	PropKeyFreeToneMarking              = "tone_free_marking"
	PropKeyEnableSpellCheck             = "enable_spell_check"
	PropKeySpellCheckByRules            = "spell_check_by_rules"
	PropKeySpellCheckByDicts            = "spell_check_by_dicts"
	PropKeyMacroEnabled                 = "macro_enabled"
	PropKeyMacroTable                   = "open_macro_table"
	PropKeyEmojiEnabled                 = "emoji_enabled"
	PropKeyConfiguration                = "configuration"
	PropKeyInputModeLookupTable         = "input_mode_lookup_table"
	PropKeyInputModeLookupTableShortcut = "input_mode_lookup_table_shortcut"
	PropKeyAutoCapitalizeMacro          = "auto_capitalize_macro"
	PropKeyIMQuickSwitchEnabled         = "im_quick_switch"
	PropKeyRestoreKeyStrokes         = "restore_key_strokes"
	PropKeyDisableOnGame             = "disable_on_game"
)

var IBusSeparator = &ibus.Property{
	Name:      "IBusProperty",
	Key:       "-",
	Type:      ibus.PROP_TYPE_SEPARATOR,
	Label:     dbus.MakeVariant(ibus.NewText("")),
	Tooltip:   dbus.MakeVariant(ibus.NewText("")),
	Sensitive: true,
	Visible:   true,
	Symbol:    dbus.MakeVariant(ibus.NewText("")),
	SubProps:  dbus.MakeVariant(*ibus.NewPropList()),
}

func GetPropListByConfig(c *config.Config) *ibus.PropList {
	var props []*ibus.Property
	if c.DefaultInputMode != config.UsIM {
		props = append(props,
			IBusSeparator,
			&ibus.Property{
				Name:      "IBusProperty",
				Key:       "-",
				Type:      ibus.PROP_TYPE_MENU,
				Label:     dbus.MakeVariant(ibus.NewText("Bảng mã")),
				Tooltip:   dbus.MakeVariant(ibus.NewText("Bảng mã")),
				Sensitive: true,
				Visible:   true,
				Icon:      "fonts",
				Symbol:    dbus.MakeVariant(ibus.NewText("")),
				SubProps:  dbus.MakeVariant(GetCharsetPropListByConfig(c)),
			},
			&ibus.Property{
				Name:      "IBusProperty",
				Key:       "-",
				Type:      ibus.PROP_TYPE_MENU,
				Label:     dbus.MakeVariant(ibus.NewText("Kiểu gõ")),
				Tooltip:   dbus.MakeVariant(ibus.NewText("Kiểu gõ")),
				Sensitive: true,
				Visible:   true,
				Icon:      "preferences-desktop",
				Symbol:    dbus.MakeVariant(ibus.NewText("")),
				SubProps:  dbus.MakeVariant(GetIMPropListByConfig(c)),
			},
		)
	}
	isVi := c.DefaultInputMode != config.UsIM
	props = append(props,
		&ibus.Property{
			Name:      "IBusProperty",
			Key:       "-",
			Type:      ibus.PROP_TYPE_MENU,
			Label:     dbus.MakeVariant(ibus.NewText("Nâng cao")),
			Tooltip:   dbus.MakeVariant(ibus.NewText("Nâng cao")),
			Sensitive: true,
			Visible:   true,
			Icon:      "preferences-other",
			Symbol:    dbus.MakeVariant(ibus.NewText("")),
			SubProps:  dbus.MakeVariant(func() *ibus.PropList { if isVi { return GetAdvancedPropList(c) }; return GetAdvancedPropListEnglish(c) }()),
		},
	)
	props = append(props,
		&ibus.Property{
			Name:      "IBusProperty",
			Key:       PropKeyInputModeLookupTableShortcut,
			Type:      ibus.PROP_TYPE_NORMAL,
			Label:     dbus.MakeVariant(ibus.NewText("KTelex Settings")),
			Tooltip:   dbus.MakeVariant(ibus.NewText("KTelex Settings")),
			Sensitive: true,
			Visible:   true,
			Icon:      "appointment",
			Symbol:    dbus.MakeVariant(ibus.NewText("")),
			SubProps:  dbus.MakeVariant(*ibus.NewPropList()),
		},
	)
	return ibus.NewPropList(props...)
}

func GetCharsetPropListByConfig(c *config.Config) *ibus.PropList {
	var charsetProperties []*ibus.Property
	charsetProperties = append(charsetProperties,
		IBusSeparator)
	for _, charset := range bamboo.GetCharsetNames() {
		var state = ibus.PROP_STATE_UNCHECKED
		if charset == c.OutputCharset {
			state = ibus.PROP_STATE_CHECKED
		}
		var imProp = &ibus.Property{
			Name:      "IBusProperty",
			Key:       "OutputCharset::" + charset,
			Type:      ibus.PROP_TYPE_RADIO,
			Label:     dbus.MakeVariant(ibus.NewText(charset)),
			Tooltip:   dbus.MakeVariant(ibus.NewText("OutputCharset: " + charset)),
			Sensitive: true,
			Visible:   true,
			State:     state,
			Symbol:    dbus.MakeVariant(ibus.NewText("U")),
			SubProps:  dbus.MakeVariant(*ibus.NewPropList()),
		}
		charsetProperties = append(charsetProperties, imProp)
	}
	return ibus.NewPropList(charsetProperties...)
}

func GetIMPropListByConfig(c *config.Config) *ibus.PropList {
	var imProperties []*ibus.Property
	imProperties = append(imProperties,
		&ibus.Property{
			Name:      "IBusProperty",
			Key:       PropKeyConfiguration,
			Type:      ibus.PROP_TYPE_NORMAL,
			Label:     dbus.MakeVariant(ibus.NewText("Tự định nghĩa kiểu gõ")),
			Tooltip:   dbus.MakeVariant(ibus.NewText("Tự định nghĩa kiểu gõ")),
			Sensitive: true,
			Visible:   true,
			Symbol:    dbus.MakeVariant(ibus.NewText("BC")),
			SubProps:  dbus.MakeVariant(*ibus.NewPropList()),
		},
		IBusSeparator,
	)
	for im := range c.InputMethodDefinitions {
		var state = ibus.PROP_STATE_UNCHECKED
		if im == c.InputMethod {
			state = ibus.PROP_STATE_CHECKED
		}
		var imProp = &ibus.Property{
			Name:      "IBusProperty",
			Key:       im,
			Type:      ibus.PROP_TYPE_RADIO,
			Label:     dbus.MakeVariant(ibus.NewText(im)),
			Tooltip:   dbus.MakeVariant(ibus.NewText("Kiểu gõ " + im)),
			Sensitive: true,
			Visible:   true,
			State:     state,
			Symbol:    dbus.MakeVariant(ibus.NewText("V")),
			SubProps:  dbus.MakeVariant(*ibus.NewPropList()),
		}
		imProperties = append(imProperties, imProp)
	}
	return ibus.NewPropList(imProperties...)
}

func GetMacroPropListByConfig(c *config.Config) *ibus.PropList {
	macroChecked := ibus.PROP_STATE_UNCHECKED
	autoCapitalizeMacro := ibus.PROP_STATE_UNCHECKED

	if c.IBflags&config.IBmacroEnabled != 0 {
		macroChecked = ibus.PROP_STATE_CHECKED
	}
	if c.IBflags&config.IBautoCapitalizeMacro != 0 {
		autoCapitalizeMacro = ibus.PROP_STATE_CHECKED
	}
	return ibus.NewPropList(
		&ibus.Property{
			Name:      "IBusProperty",
			Key:       PropKeyMacroEnabled,
			Type:      ibus.PROP_TYPE_TOGGLE,
			Label:     dbus.MakeVariant(ibus.NewText("Bật gõ tắt")),
			Tooltip:   dbus.MakeVariant(ibus.NewText("Bật gõ tắt")),
			Sensitive: true,
			Visible:   true,
			State:     macroChecked,
			Symbol:    dbus.MakeVariant(ibus.NewText("M")),
			SubProps:  dbus.MakeVariant(*ibus.NewPropList()),
		},
		&ibus.Property{
			Name:      "IBusProperty",
			Key:       PropKeyAutoCapitalizeMacro,
			Type:      ibus.PROP_TYPE_TOGGLE,
			Label:     dbus.MakeVariant(ibus.NewText("Tự động viết hoa")),
			Tooltip:   dbus.MakeVariant(ibus.NewText("Auto capitalize macro")),
			Sensitive: true,
			Visible:   true,
			State:     autoCapitalizeMacro,
			Symbol:    dbus.MakeVariant(ibus.NewText("C")),
			SubProps:  dbus.MakeVariant(*ibus.NewPropList()),
		},
	)
}

func GetSpellCheckingPropListByConfig(c *config.Config) *ibus.PropList {
	spellCheckByRules := ibus.PROP_STATE_UNCHECKED
	spellCheckByDicts := ibus.PROP_STATE_UNCHECKED

	// spelling
	spellingChecked := ibus.PROP_STATE_UNCHECKED
	if c.IBflags&config.IBspellCheckEnabled != 0 {
		spellingChecked = ibus.PROP_STATE_CHECKED
	}
	if c.IBflags&config.IBspellCheckWithRules != 0 {
		spellCheckByRules = ibus.PROP_STATE_CHECKED
	}
	if c.IBflags&config.IBspellCheckWithDicts != 0 {
		spellCheckByDicts = ibus.PROP_STATE_CHECKED
	}
	rulesSensitive := spellingChecked == ibus.PROP_STATE_CHECKED
	dictsSensitive := spellingChecked == ibus.PROP_STATE_CHECKED
	return ibus.NewPropList(
		&ibus.Property{
			Name:      "IBusProperty",
			Key:       PropKeyEnableSpellCheck,
			Type:      ibus.PROP_TYPE_TOGGLE,
			Label:     dbus.MakeVariant(ibus.NewText("Bật kiểm tra chính tả")),
			Tooltip:   dbus.MakeVariant(ibus.NewText("")),
			Sensitive: true,
			Visible:   true,
			State:     spellingChecked,
			Symbol:    dbus.MakeVariant(ibus.NewText("S")),
			SubProps:  dbus.MakeVariant(*ibus.NewPropList()),
		},
		IBusSeparator,
		&ibus.Property{
			Name:      "IBusProperty",
			Key:       PropKeySpellCheckByRules,
			Type:      ibus.PROP_TYPE_TOGGLE,
			Label:     dbus.MakeVariant(ibus.NewText("Sử dụng luật ghép vần")),
			Tooltip:   dbus.MakeVariant(ibus.NewText("Sử dụng luật ghép vần")),
			Sensitive: rulesSensitive,
			Visible:   true,
			State:     spellCheckByRules,
			Symbol:    dbus.MakeVariant(ibus.NewText("M")),
			SubProps:  dbus.MakeVariant(*ibus.NewPropList()),
		},
		&ibus.Property{
			Name:      "IBusProperty",
			Key:       PropKeySpellCheckByDicts,
			Type:      ibus.PROP_TYPE_TOGGLE,
			Label:     dbus.MakeVariant(ibus.NewText("Sử dụng từ điển")),
			Tooltip:   dbus.MakeVariant(ibus.NewText("Sử dụng từ điển")),
			Sensitive: dictsSensitive,
			Visible:   true,
			State:     spellCheckByDicts,
			Symbol:    dbus.MakeVariant(ibus.NewText("O")),
			SubProps:  dbus.MakeVariant(*ibus.NewPropList()),
		},
	)
}

func GetAdvancedPropList(c *config.Config) *ibus.PropList {
	// Full advanced: macro + options (for VI engine)
	var props []dbus.Variant
	props = append(props, GetMacroPropListByConfig(c).PropertyList...)
	props = append(props, GetOptionsPropListByConfig(c).PropertyList...)
	return &ibus.PropList{
		Name:         "IBusPropList",
		PropertyList: props,
	}
}

func GetAdvancedPropListEnglish(c *config.Config) *ibus.PropList {
	// English engines: macro + options (game toggle, tone, etc.)
	var props []dbus.Variant
	props = append(props, GetMacroPropListByConfig(c).PropertyList...)
	props = append(props, GetOptionsPropListByConfig(c).PropertyList...)
	return &ibus.PropList{
		Name:         "IBusPropList",
		PropertyList: props,
	}
}

func GetOptionsPropListByConfig(c *config.Config) *ibus.PropList {
	disableOnGameChecked := ibus.PROP_STATE_UNCHECKED
	if c.IBflags&config.IBdisableOnGame != 0 {
		disableOnGameChecked = ibus.PROP_STATE_CHECKED
	}
	// tone
	toneStdChecked := ibus.PROP_STATE_UNCHECKED
	toneFreeMarkingChecked := ibus.PROP_STATE_UNCHECKED
	if c.Flags&bamboo.EstdToneStyle != 0 {
		toneStdChecked = ibus.PROP_STATE_CHECKED
	}
	if c.Flags&bamboo.EfreeToneMarking != 0 {
		toneFreeMarkingChecked = ibus.PROP_STATE_CHECKED
	}

	return ibus.NewPropList(
		&ibus.Property{
			Name:      "IBusProperty",
			Key:       PropKeyDisableOnGame,
			Type:      ibus.PROP_TYPE_TOGGLE,
			Label:     dbus.MakeVariant(ibus.NewText("Tắt trong game")),
			Tooltip:   dbus.MakeVariant(ibus.NewText("Tự động tắt gõ tiếng Việt trong game (Steam, Heroic, Lutris, Wine)")),
			Sensitive: true,
			Visible:   true,
			State:     disableOnGameChecked,
			Symbol:    dbus.MakeVariant(ibus.NewText("G")),
			SubProps:  dbus.MakeVariant(*ibus.NewPropList()),
		},
		IBusSeparator,
		&ibus.Property{
			Name:      "IBusProperty",
			Key:       PropKeyFreeToneMarking,
			Type:      ibus.PROP_TYPE_TOGGLE,
			Label:     dbus.MakeVariant(ibus.NewText("Bỏ dấu tự do")),
			Tooltip:   dbus.MakeVariant(ibus.NewText("Bỏ dấu tự do")),
			Sensitive: true,
			Visible:   true,
			State:     toneFreeMarkingChecked,
			Symbol:    dbus.MakeVariant(ibus.NewText("M")),
			SubProps:  dbus.MakeVariant(*ibus.NewPropList()),
		},
		&ibus.Property{
			Name:      "IBusProperty",
			Key:       PropKeyStdToneStyle,
			Type:      ibus.PROP_TYPE_TOGGLE,
			Label:     dbus.MakeVariant(ibus.NewText("Dấu thanh chuẩn")),
			Tooltip:   dbus.MakeVariant(ibus.NewText("Use òa, úy... (instead of oà, uý)")),
			Sensitive: true,
			Visible:   true,
			State:     toneStdChecked,
			Symbol:    dbus.MakeVariant(ibus.NewText("M")),
			SubProps:  dbus.MakeVariant(*ibus.NewPropList()),
		},
	)
}
