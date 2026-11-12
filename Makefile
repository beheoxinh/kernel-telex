#
# Bamboo - A Vietnamese Input method editor
# Copyright (C) 2018 Luong Thanh Lam <ltlam93@gmail.com>
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU General Public License for more details.
#
# You should have received a copy of the GNU General Public License
# along with this program.  If not, see <http://www.gnu.org/licenses/>.
#

SHELL=/bin/sh

engine_name=ktelex
engine_gui_name=ibus-setup-KTelex.desktop
ibus_e_name=ibus-engine-$(engine_name)

PREFIX ?= /usr
DIST ?= dist

engine_dir=$(PREFIX)/share/$(engine_name)
ibus_dir=$(PREFIX)/share/ibus

all: build

# Always rebuild: Go's own cache makes this cheap.
build:
	DIST=$(DIST) bash scripts/build

# `sudo make install` only copies; it builds (as root) only if nothing was built.
$(DIST)/$(ibus_e_name):
	DIST=$(DIST) bash scripts/build

test:
	bash scripts/test

clean:
	rm -rf $(DIST)
	rm -f $(ibus_e_name) ktelex

install: $(DIST)/$(ibus_e_name)
	DIST=$(DIST) bash scripts/install $(PREFIX) $(DESTDIR)

# Files only; ./uninstall.sh also removes the udev rule, GNOME source and extension.
uninstall:
	rm -rf $(DESTDIR)$(engine_dir)
	rm -f $(DESTDIR)$(PREFIX)/libexec/$(ibus_e_name)
	rm -f $(DESTDIR)$(PREFIX)/libexec/ktelex-uinput
	rm -f $(DESTDIR)$(ibus_dir)/component/$(engine_name).xml
	rm -f $(DESTDIR)$(PREFIX)/share/applications/$(engine_gui_name)

.PHONY: all build test clean install uninstall
