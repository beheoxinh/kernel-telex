#!/usr/bin/env bash
# ==============================================================================
# install.sh - Build and install KTelex in one go.
#   git clone https://github.com/beheoxinh/kernel-telex.git && cd kernel-telex
#   ./prepare.sh   # first time only: build dependencies
#   ./install.sh   # build into dist/, install (sudo), set up /dev/uinput, restart IBus
# Run as your normal user: the build stays user-owned, only the copy uses sudo.
# Env: KTELEX_NO_INPUT_SOURCE=1 skips adding KTelex to GNOME input sources;
#      KTELEX_NO_EXTENSION=1 skips the GNOME focus-tracking extension.
# ==============================================================================
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()    { echo -e "${BLUE}[INFO]${NC} $*"; }
success() { echo -e "${GREEN}[OK]${NC} $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }
error()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }

run_sudo() {
    if [ "$(id -u)" -eq 0 ]; then
        "$@"
    elif command -v sudo >/dev/null 2>&1; then
        sudo "$@"
    else
        error "Cần quyền root hoặc sudo để chạy: $*"
        exit 1
    fi
}

# The engine has its paths compiled in (/usr/libexec, /usr/share/ktelex).
PREFIX=/usr
CURRENT_USER="${SUDO_USER:-$USER}"

if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_USER:-}" ]; then
    warn "Đang chạy bằng sudo: nên chạy './install.sh' bằng user thường (script tự gọi sudo khi cần)."
fi

# Distro golang-go may be older than go.mod needs: prefer a newer versioned
# toolchain (/usr/lib/go-1.2x, installed by prepare.sh on Debian/Ubuntu).
go_minor_of() { "$1" env GOVERSION 2>/dev/null | sed -E 's/^go1\.([0-9]+).*/\1/'; }
if ! command -v go >/dev/null 2>&1 || [ "$(go_minor_of go)" -lt 23 ] 2>/dev/null; then
    for d in /usr/lib/go-1.2[3-9] /usr/lib/go-1.[3-9][0-9] /usr/local/go /snap/go/current; do
        m="$(go_minor_of "$d/bin/go" 2>/dev/null || true)"
        if [ -n "$m" ] && [ "$m" -ge 23 ] 2>/dev/null; then export PATH="$d/bin:$PATH"; break; fi
    done
fi

# 1. Kiểm tra công cụ và thư viện build
info "Kiểm tra công cụ biên dịch..."
missing=()
for tool in go make gcc pkg-config ibus; do
    command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
done
if command -v pkg-config >/dev/null 2>&1; then
    for lib in gtk+-3.0 x11 xtst; do
        pkg-config --exists "$lib" || missing+=("$lib (dev)")
    done
fi
if [ ${#missing[@]} -gt 0 ]; then
    error "Thiếu: ${missing[*]}"
    error "Chạy './prepare.sh' để tự cài đủ phụ thuộc, rồi chạy lại './install.sh'."
    exit 1
fi
go_minor="$(go env GOVERSION | sed -E 's/^go1\.([0-9]+).*/\1/')"
if [ "${go_minor:-0}" -lt 23 ] 2>/dev/null; then
    error "Cần Go >= 1.23 (hiện có $(go env GOVERSION))."
    exit 1
fi

# 2. Biên dịch (ra dist/)
info "Biên dịch KTelex..."
make clean >/dev/null
make build
[ -x dist/ibus-engine-ktelex ] || { error "Biên dịch thất bại: thiếu dist/ibus-engine-ktelex"; exit 1; }
success "Biên dịch thành công!"

# 3. Cài đặt vào hệ thống (chỉ copy, không build lại bằng root)
info "Cài đặt vào ${PREFIX} (cần sudo)..."
run_sudo bash scripts/install "${PREFIX}"

# 4. Quyền /dev/uinput cho chế độ Kernel
info "Thiết lập quyền /dev/uinput..."
if ! lsmod | grep -q "^uinput" && [ ! -e /dev/uinput ]; then
    run_sudo modprobe uinput || warn "Không nạp được module uinput."
fi
if [ -d /etc/modules-load.d ] && [ ! -f /etc/modules-load.d/uinput.conf ]; then
    echo "uinput" | run_sudo tee /etc/modules-load.d/uinput.conf >/dev/null
fi

UDEV_RULE_FILE="/etc/udev/rules.d/99-uinput-ktelex.rules"
UDEV_RULE='KERNEL=="uinput", MODE="0660", GROUP="input", OPTIONS+="static_node=uinput", TAG+="uaccess"'
if [ "$(cat "$UDEV_RULE_FILE" 2>/dev/null)" != "$UDEV_RULE" ]; then
    info "Ghi udev rule ${UDEV_RULE_FILE}..."
    echo "$UDEV_RULE" | run_sudo tee "$UDEV_RULE_FILE" >/dev/null
    run_sudo udevadm control --reload-rules || true
    run_sudo udevadm trigger --subsystem-match=misc --sysname-match=uinput || true
fi

relogin=0
if getent group input >/dev/null 2>&1 && ! id -nG "$CURRENT_USER" | grep -qw input; then
    info "Thêm user '${CURRENT_USER}' vào nhóm 'input'..."
    run_sudo usermod -aG input "$CURRENT_USER"
    relogin=1
fi
# Access right now, without logging out. Only this user: a world-writable
# uinput would let any local process inject keystrokes into every session.
if [ -e /dev/uinput ]; then
    if ! { command -v setfacl >/dev/null 2>&1 && run_sudo setfacl -m "u:${CURRENT_USER}:rw" /dev/uinput 2>/dev/null; }; then
        run_sudo chgrp input /dev/uinput 2>/dev/null || true
        run_sudo chmod 0660 /dev/uinput 2>/dev/null || true
    fi
fi

# 5. Cache desktop
if command -v update-desktop-database >/dev/null 2>&1; then
    run_sudo update-desktop-database "${PREFIX}/share/applications" 2>/dev/null || true
fi

# 6. IBus: nạp lại component, thêm KTelex vào nguồn nhập của GNOME
if pgrep -x ibus-daemon >/dev/null 2>&1; then
    info "Khởi động lại IBus..."
    ibus write-cache 2>/dev/null || true
    ibus restart 2>/dev/null || warn "Không chạy được 'ibus restart' (hãy chạy trong phiên đồ họa)."
else
    info "IBus chưa chạy trong phiên này; nó sẽ nạp KTelex ở lần khởi động tới."
fi

if [ "${KTELEX_NO_INPUT_SOURCE:-0}" != 1 ] && [ "$(id -u)" -ne 0 ] \
    && command -v gsettings >/dev/null 2>&1 \
    && gsettings list-keys org.gnome.desktop.input-sources >/dev/null 2>&1; then
    sources="$(gsettings get org.gnome.desktop.input-sources sources)"
    if [[ "$sources" != *"'KTelex'"* ]]; then
        if [[ "$sources" == *"[]"* ]]; then
            new="[('ibus', 'KTelex')]"
        else
            new="${sources%]}, ('ibus', 'KTelex')]"
        fi
        gsettings set org.gnome.desktop.input-sources sources "$new" \
            && success "Đã thêm KTelex vào nguồn nhập GNOME (Super+Space để chuyển)."
    fi
fi

# 7. GNOME Wayland: extension cho biết cửa sổ đang focus (để nhận diện app/game)
EXT_UUID="wmclass-helper@local"
if [ "${KTELEX_NO_EXTENSION:-0}" != 1 ] && [ "$(id -u)" -ne 0 ] && command -v gnome-extensions >/dev/null 2>&1 \
    && [ -d "gnome-extension/${EXT_UUID}" ]; then
    ext_dir="${XDG_DATA_HOME:-$HOME/.local/share}/gnome-shell/extensions/${EXT_UUID}"
    mkdir -p "$ext_dir"
    if ! cmp -s "gnome-extension/${EXT_UUID}/extension.js" "$ext_dir/extension.js" 2>/dev/null \
        || ! cmp -s "gnome-extension/${EXT_UUID}/metadata.json" "$ext_dir/metadata.json" 2>/dev/null; then
        install -m 0644 "gnome-extension/${EXT_UUID}/extension.js" "gnome-extension/${EXT_UUID}/metadata.json" "$ext_dir/"
        ext_new=1
    fi
    if gnome-extensions enable "$EXT_UUID" 2>/dev/null; then
        success "Đã bật GNOME extension ${EXT_UUID}."
    else
        ext_new=1
    fi
    if [ "${ext_new:-0}" = 1 ] && [ "${XDG_SESSION_TYPE:-}" = wayland ]; then
        warn "GNOME Wayland chỉ nạp extension mới sau khi đăng xuất/đăng nhập lại; sau đó chạy: gnome-extensions enable ${EXT_UUID}"
    fi
fi

echo ""
success "=================================================================="
success "       CÀI ĐẶT THÀNH CÔNG KERNEL TELEX (KTELEX)!"
success "=================================================================="
echo -e "  • Chuyển Anh/Việt: phím chuyển nguồn nhập của GNOME (${BLUE}Super+Space${NC}, hoặc ${BLUE}Caps Lock${NC} nếu bật grp:caps_toggle)."
echo -e "  • Đổi chế độ gõ cho từng app: ${BLUE}Alt+Z${NC} (sửa trong phần Cài đặt)."
echo -e "  • Cài đặt: tìm 'KTelex' trong menu ứng dụng, hoặc ${BLUE}/usr/libexec/ibus-engine-ktelex --gui${NC}."
echo -e "  • Log: ${BLUE}\${XDG_STATE_HOME:-~/.local/state}/ktelex/ktelex.log${NC} (log chi tiết: KTELEX_DEBUG=1 hoặc tạo file ~/.config/ibus-ktelex/debug)"
echo -e "  • Gỡ cài đặt: ${BLUE}./uninstall.sh${NC}"
if [ "$relogin" = 1 ]; then
    warn "Đăng xuất và đăng nhập lại một lần để quyền nhóm 'input' có hiệu lực lâu dài."
fi
