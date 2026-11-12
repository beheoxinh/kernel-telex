#!/usr/bin/env bash
# ==============================================================================
# prepare.sh - Cài đặt toàn bộ thư viện & phụ thuộc môi trường phát triển (Dev)
# Hỗ trợ: Fedora, Nobara, RHEL/CentOS/Rocky Linux, Arch Linux, Ubuntu/Debian
# ==============================================================================

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

info()    { echo -e "${BLUE}[INFO]${NC} $*"; }
success() { echo -e "${GREEN}[OK]${NC} $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }
error()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# Kiểm tra quyền sudo/root khi cài package
run_sudo() {
    if [ "$(id -u)" -eq 0 ]; then
        "$@"
    else
        if command -v sudo >/dev/null 2>&1; then
            sudo "$@"
        else
            error "Cần quyền root hoặc công cụ sudo để thực hiện: $*"
            exit 1
        fi
    fi
}

info "Bắt đầu kiểm tra và cài đặt dependencies cho dự án Kernel-Telex..."

# 1. Phát hiện hệ điều hành
OS_ID=""
if [ -f /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    OS_ID="${ID:-}"
    OS_LIKE="${ID_LIKE:-}"
else
    error "Không tìm thấy /etc/os-release. Chưa thể nhận diện hệ điều hành."
    exit 1
fi

info "Phát hiện hệ điều hành: ${PRETTY_NAME:-$OS_ID}"

# 2. Cài đặt package theo hệ điều hành
case "$OS_ID" in
    fedora|nobara|rhel|centos|rocky|almalinux)
        info "Cài đặt các gói phát triển qua dnf..."
        DNF_CMD="dnf"
        if ! command -v dnf >/dev/null 2>&1 && command -v dnf5 >/dev/null 2>&1; then
            DNF_CMD="dnf5"
        fi

        PKGS=(
            golang
            make
            gcc
            pkg-config
            gtk3-devel
            ibus-devel
            libX11-devel
            libXtst-devel
        )
        run_sudo "$DNF_CMD" install -y "${PKGS[@]}"
        ;;

    arch|manjaro|endeavouros)
        info "Cài đặt các gói phát triển qua pacman..."
        PKGS=(
            go
            make
            gcc
            pkgconf
            gtk3
            ibus
            libx11
            libxtst
        )
        run_sudo pacman -Sy --needed --noconfirm "${PKGS[@]}"
        ;;

    ubuntu|debian|pop|linuxmint)
        info "Cài đặt các gói phát triển qua apt..."
        run_sudo apt-get update
        PKGS=(
            golang-go
            make
            gcc
            pkg-config
            libgtk-3-dev
            libibus-1.0-dev
            libx11-dev
            libxtst-dev
        )
        run_sudo apt-get install -y "${PKGS[@]}"
        ;;

    *)
        if [[ "${OS_LIKE:-}" == *"fedora"* ]] || [[ "${OS_LIKE:-}" == *"rhel"* ]]; then
            info "Hệ điều hành tương thích Fedora. Dùng dnf..."
            run_sudo dnf install -y golang make gcc pkg-config gtk3-devel ibus-devel libX11-devel libXtst-devel
        elif [[ "${OS_LIKE:-}" == *"debian"* ]] || [[ "${OS_LIKE:-}" == *"ubuntu"* ]]; then
            info "Hệ điều hành tương thích Debian. Dùng apt..."
            run_sudo apt-get update && run_sudo apt-get install -y golang-go make gcc pkg-config libgtk-3-dev libibus-1.0-dev libx11-dev libxtst-dev
        elif [[ "${OS_LIKE:-}" == *"arch"* ]]; then
            info "Hệ điều hành tương thích Arch. Dùng pacman..."
            run_sudo pacman -Sy --needed --noconfirm go make gcc pkgconf gtk3 ibus libx11 libxtst
        else
            warn "Chưa tự động nhận diện được package manager của: $OS_ID ($OS_LIKE)"
            warn "Hãy đảm bảo cài đặt thủ công: go (>=1.23), make, gcc, pkg-config, gtk3-devel, ibus-devel, libX11-devel, libXtst-devel."
        fi
        ;;
esac

# 2b. Go >= 1.23 (go.mod). Ubuntu 24.04 / Debian 12 ship an older golang-go:
#     fall back to the versioned golang-1.2x package, or tell the user.
go_ok() {
    local g="$1" minor
    minor="$("$g" env GOVERSION 2>/dev/null | sed -E 's/^go1\.([0-9]+).*/\1/')"
    [ -n "$minor" ] && [ "$minor" -ge 23 ] 2>/dev/null
}
if ! command -v go >/dev/null 2>&1 || ! go_ok go; then
    found=""
    for d in /usr/lib/go-1.2[3-9] /usr/lib/go-1.[3-9][0-9]; do
        [ -x "$d/bin/go" ] && go_ok "$d/bin/go" && found="$d/bin"
    done
    if [ -z "$found" ] && command -v apt-get >/dev/null 2>&1; then
        for v in 1.24 1.23; do
            if apt-cache show "golang-$v-go" >/dev/null 2>&1; then
                info "golang-go quá cũ: cài golang-$v-go..."
                run_sudo apt-get install -y "golang-$v-go" && found="/usr/lib/go-$v/bin" && break
            fi
        done
    fi
    if [ -n "$found" ]; then
        export PATH="$found:$PATH"
        success "Dùng Go tại $found (install.sh tự tìm thấy đường dẫn này)."
    else
        warn "Cần Go >= 1.23. Cài từ https://go.dev/dl/ hoặc 'sudo snap install go --classic', rồi chạy lại."
    fi
fi

# 3. Cấu hình quyền truy cập /dev/uinput (Cực kỳ quan trọng cho Kernel Mode)
info "Kiểm tra quyền truy cập /dev/uinput (Kernel Mode)..."

# Nạp module uinput nếu chưa có
if ! lsmod | grep -q "^uinput"; then
    info "Đang nạp module hạt nhân uinput..."
    run_sudo modprobe uinput || warn "Không thể modprobe uinput tự động. Hãy đảm bảo kernel hỗ trợ uinput."
fi

# Đảm bảo module tự động nạp lúc khởi động
if [ -d /etc/modules-load.d ]; then
    if [ ! -f /etc/modules-load.d/uinput.conf ]; then
        info "Tạo /etc/modules-load.d/uinput.conf..."
        echo "uinput" | run_sudo tee /etc/modules-load.d/uinput.conf >/dev/null
    fi
fi

# Cấu hình udev rule cho /dev/uinput để user thường (hoặc nhóm input) có thể đọc/ghi trực tiếp
CURRENT_USER="${SUDO_USER:-$USER}"
UDEV_RULE_FILE="/etc/udev/rules.d/99-uinput-ktelex.rules"

info "Thiết lập udev rule cho /dev/uinput tại ${UDEV_RULE_FILE}..."
# Cấp quyền rw cho nhóm input và gắn tag uaccess cho session người dùng
cat << 'UDEV_EOF' | run_sudo tee "$UDEV_RULE_FILE" >/dev/null
KERNEL=="uinput", MODE="0660", GROUP="input", OPTIONS+="static_node=uinput", TAG+="uaccess"
UDEV_EOF

run_sudo udevadm control --reload-rules || true
run_sudo udevadm trigger || true

# Thêm user hiện tại vào group input nếu chưa có
if getent group input >/dev/null 2>&1; then
    if ! id -nG "$CURRENT_USER" | grep -qw "input"; then
        info "Thêm người dùng '${CURRENT_USER}' vào nhóm 'input'..."
        run_sudo usermod -aG input "$CURRENT_USER"
        warn "LƯU Ý: Bạn cần đăng xuất và đăng nhập lại (hoặc chạy 'newgrp input') để nhóm 'input' có hiệu lực!"
    fi
fi

# Cấp ACL tạm thời cho user ngay trong phiên hiện tại để có thể dev ngay không cần relogin
if command -v setfacl >/dev/null 2>&1 && [ -e /dev/uinput ]; then
    run_sudo setfacl -m "u:${CURRENT_USER}:rw" /dev/uinput 2>/dev/null || true
fi

# 4. Kiểm tra Go dependencies trong dự án
info "Kiểm tra mã nguồn và vendor Go modules..."
if command -v go >/dev/null 2>&1; then
    GO_VER="$(go version)"
    info "Đang sử dụng: $GO_VER"
    # Đồng bộ vendor nếu thiếu
    if [ ! -d "vendor" ]; then
        info "Chạy 'go mod vendor' để tải và chuẩn bị dependencies..."
        go mod vendor
    fi
else
    warn "Chưa phát hiện lệnh 'go' trong PATH hiện tại. Hãy kiểm tra lại môi trường PATH."
fi

success "Hoàn tất chuẩn bị môi trường phát triển (Dev Environment Ready)!"
info "Bạn có thể kiểm tra build ngay bằng lệnh:"
echo -e "    ${BLUE}make test${NC}"
echo -e "    ${BLUE}make build${NC}"
