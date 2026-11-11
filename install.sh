#!/usr/bin/env bash
# ==============================================================================
# install.sh - Biên dịch và cài đặt hoàn chỉnh KTelex vào hệ thống
# Hỗ trợ: Fedora, Nobara, RHEL, CentOS, Arch, Debian/Ubuntu...
# ==============================================================================

set -euo pipefail

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
    else
        if command -v sudo >/dev/null 2>&1; then
            sudo "$@"
        else
            error "Cần quyền root hoặc công cụ sudo để thực hiện: $*"
            exit 1
        fi
    fi
}

PREFIX="${PREFIX:-/usr}"
CURRENT_USER="${SUDO_USER:-$USER}"

info "Bắt đầu quy trình cài đặt KTelex (Prefix: ${PREFIX})..."

# 1. Kiểm tra các công cụ biên dịch tối thiểu
info "Kiểm tra công cụ biên dịch..."
MISSING_TOOLS=()
for tool in go make gcc pkg-config ibus; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        MISSING_TOOLS+=("$tool")
    fi
done

if [ ${#MISSING_TOOLS[@]} -gt 0 ]; then
    error "Thiếu các công cụ sau: ${MISSING_TOOLS[*]}"
    error "Vui lòng chạy './prepare.sh' trước để tự động cài đặt đầy đủ phụ thuộc!"
    exit 1
fi

# 2. Biên dịch binary
info "Biên dịch dự án KTelex..."
make clean
make build

if [ ! -f "ibus-engine-ktelex" ]; then
    error "Biên dịch thất bại: không tìm thấy file 'ibus-engine-ktelex' sau khi build!"
    exit 1
fi
success "Biên dịch thành công!"

# 3. Biên dịch standalone uinput-server (nếu có)
if [ -d "cmd/uinput-server" ]; then
    info "Biên dịch phụ trợ cmd/uinput-server..."
    go build -o ktelex-uinput ./cmd/uinput-server 2>/dev/null || warn "Bỏ qua build ktelex-uinput (không bắt buộc)."
fi

# 4. Cài đặt các file vào hệ thống
info "Cài đặt files vào hệ thống (cần quyền root/sudo)..."
run_sudo make PREFIX="${PREFIX}" install

# Nếu có ktelex-uinput thì cài vào libexec
if [ -f "ktelex-uinput" ]; then
    run_sudo cp -f "ktelex-uinput" "${PREFIX}/libexec/" 2>/dev/null || true
fi

# 5. Cấu hình quyền truy cập /dev/uinput (Cực kỳ quan trọng để chế độ Kernel hoạt động)
info "Thiết lập quyền truy cập hạt nhân /dev/uinput..."
if ! lsmod | grep -q "^uinput"; then
    run_sudo modprobe uinput || warn "Không thể nạp module uinput."
fi

if [ -d /etc/modules-load.d ] && [ ! -f /etc/modules-load.d/uinput.conf ]; then
    echo "uinput" | run_sudo tee /etc/modules-load.d/uinput.conf >/dev/null
fi

UDEV_RULE_FILE="/etc/udev/rules.d/99-uinput-ktelex.rules"
if [ ! -f "$UDEV_RULE_FILE" ]; then
    info "Tạo udev rule: ${UDEV_RULE_FILE}..."
    cat << 'UDEV_EOF' | run_sudo tee "$UDEV_RULE_FILE" >/dev/null
KERNEL=="uinput", MODE="0660", GROUP="input", OPTIONS+="static_node=uinput", TAG+="uaccess"
UDEV_EOF
    run_sudo udevadm control --reload-rules || true
    run_sudo udevadm trigger || true
fi

if getent group input >/dev/null 2>&1; then
    if ! id -nG "$CURRENT_USER" | grep -qw "input"; then
        info "Thêm user '${CURRENT_USER}' vào nhóm 'input'..."
        run_sudo usermod -aG input "$CURRENT_USER"
        warn "LƯU Ý: Bạn cần đăng xuất và đăng nhập lại để quyền nhóm 'input' có hiệu lực đầy đủ!"
    fi
fi

# Đảm bảo quyền truy cập ngay lập tức cho user hiện tại
if [ -e /dev/uinput ]; then
    if command -v setfacl >/dev/null 2>&1; then
        run_sudo setfacl -m "u:${CURRENT_USER}:rw" /dev/uinput 2>/dev/null || true
    fi
    run_sudo chmod 0666 /dev/uinput 2>/dev/null || true
fi

# 6. Cập nhật cache desktop & icon
info "Cập nhật cache hệ thống (desktop databases)..."
if command -v update-desktop-database >/dev/null 2>&1; then
    run_sudo update-desktop-database "${PREFIX}/share/applications" 2>/dev/null || true
fi

# 7. Khởi động lại IBus daemon (nếu đang chạy trong user session)
info "Cấu hình IBus..."
if command -v ibus >/dev/null 2>&1; then
    if pgrep -x "ibus-daemon" >/dev/null 2>&1; then
        info "Đang khởi động lại IBus daemon..."
        ibus restart 2>/dev/null || warn "Không thể chạy 'ibus restart' trực tiếp (có thể cần chạy từ terminal session GUI)."
    else
        info "IBus daemon chưa chạy hoặc ở session khác. Hãy chạy 'ibus restart' sau khi cài đặt."
    fi
fi

echo ""
success "=================================================================="
success "       CÀI ĐẶT THÀNH CÔNG KERNEL TELEX (KTELEX)!"
success "=================================================================="
echo -e "Các bước tiếp theo để kích hoạt trên Fedora / GNOME:"
echo -e "  1. Khởi động lại IBus:  ${BLUE}ibus restart${NC}"
echo -e "  2. Mở Cài đặt hệ thống (GNOME Settings) -> Keyboard -> Input Sources."
echo -e "  3. Nhấn dấu '+' -> Chọn 'Vietnamese' -> Chọn '${GREEN}KTelex${NC}'."
echo -e "  4. Mở cài đặt cấu hình KTelex: ${BLUE}ibus-setup-KTelex${NC} hoặc tìm 'KTelex' trong Application Menu."
echo -e "  5. Nếu gõ Kernel Mode chưa nhận ngay, hãy đăng xuất và đăng nhập lại để cập nhật group input."
echo ""
