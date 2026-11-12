#!/usr/bin/env bash
# ==============================================================================
# uninstall.sh - Remove everything ./install.sh set up.
#   ./uninstall.sh            # files, udev rule, GNOME input source, extension
#   ./uninstall.sh --purge    # also the user's config, macros and logs
# Run as your normal user; sudo is used only for system files.
# Kept on purpose: the user's 'input' group membership and
# /etc/modules-load.d/uinput.conf (other software may rely on them).
# ==============================================================================
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'
info()    { echo -e "${BLUE}[INFO]${NC} $*"; }
success() { echo -e "${GREEN}[OK]${NC} $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }

run_sudo() {
    if [ "$(id -u)" -eq 0 ]; then "$@"; else sudo "$@"; fi
}

purge=0
[ "${1:-}" = "--purge" ] && purge=1
PREFIX=/usr

# 1. GNOME input sources: drop the KTelex entries (keep everything else, e.g.
#    the user's own layouts and Caps Lock / Super+Space switching options).
if [ "$(id -u)" -ne 0 ] && command -v gsettings >/dev/null 2>&1 \
    && gsettings list-keys org.gnome.desktop.input-sources >/dev/null 2>&1; then
    sources="$(gsettings get org.gnome.desktop.input-sources sources)"
    if [[ "$sources" == *"'KTelex'"* || "$sources" == *"'KtelexUs'"* || "$sources" == *"'KtelexGb'"* ]]; then
        new="$(python3 - "$sources" <<'EOF'
import ast, sys
s = sys.argv[1]
if s.startswith("@a(ss) "):
    s = s[len("@a(ss) "):]
items = [t for t in ast.literal_eval(s) if not (t[0] == "ibus" and t[1] in ("KTelex", "KtelexUs", "KtelexGb"))]
print("[" + ", ".join("('%s', '%s')" % t for t in items) + "]")
EOF
)"
        if [ "$new" = "[]" ]; then
            new="[('xkb', 'us')]"
        fi
        gsettings set org.gnome.desktop.input-sources sources "$new" \
            && success "Đã gỡ KTelex khỏi nguồn nhập GNOME: $new"
    fi
fi

# 2. GNOME extension (per user)
EXT_UUID="wmclass-helper@local"
ext_dir="${XDG_DATA_HOME:-$HOME/.local/share}/gnome-shell/extensions/${EXT_UUID}"
if [ "$(id -u)" -ne 0 ] && [ -d "$ext_dir" ]; then
    command -v gnome-extensions >/dev/null 2>&1 && gnome-extensions disable "$EXT_UUID" 2>/dev/null || true
    rm -rf "$ext_dir"
    success "Đã gỡ GNOME extension ${EXT_UUID}."
fi

# 3. Installed files
info "Gỡ file đã cài trong ${PREFIX} (cần sudo)..."
run_sudo rm -rf "${PREFIX}/share/ktelex"
run_sudo rm -f "${PREFIX}/libexec/ibus-engine-ktelex" "${PREFIX}/libexec/ktelex-uinput" \
    "${PREFIX}/share/ibus/component/ktelex.xml" \
    "${PREFIX}/share/applications/ibus-setup-KTelex.desktop"

# 4. udev rule for /dev/uinput
UDEV_RULE_FILE="/etc/udev/rules.d/99-uinput-ktelex.rules"
if [ -f "$UDEV_RULE_FILE" ]; then
    run_sudo rm -f "$UDEV_RULE_FILE"
    run_sudo udevadm control --reload-rules || true
    run_sudo udevadm trigger --subsystem-match=misc --sysname-match=uinput || true
    success "Đã gỡ ${UDEV_RULE_FILE}."
fi

# 5. Per-user data
if [ "$purge" = 1 ] && [ "$(id -u)" -ne 0 ]; then
    rm -rf "$HOME/.config/ibus-ktelex" "${XDG_STATE_HOME:-$HOME/.local/state}/ktelex"
    rm -f /tmp/ktelex.log /tmp/ktelex.log.1 2>/dev/null || true
    success "Đã xoá cấu hình, macro và log của KTelex."
fi

# 6. Reload IBus so the engine disappears now
if pgrep -x ibus-daemon >/dev/null 2>&1; then
    ibus write-cache 2>/dev/null || true
    ibus restart 2>/dev/null || true
fi

success "Đã gỡ KTelex."
echo -e "  • Giữ lại: nhóm 'input' của user và ${BLUE}/etc/modules-load.d/uinput.conf${NC} (xoá tay nếu không cần)."
[ "$purge" = 1 ] || echo -e "  • Cấu hình ở ${BLUE}~/.config/ibus-ktelex${NC} vẫn còn; chạy ${BLUE}./uninstall.sh --purge${NC} để xoá."
