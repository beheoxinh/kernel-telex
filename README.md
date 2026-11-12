

# Kernel Telex (KTelex)

**Bộ gõ tiếng Việt hiệu năng cao cho Linux (GNOME / Wayland / X11) trên nền tảng IBus.**

KTelex tối ưu trải nghiệm gõ tiếng Việt trên Wayland bằng **chế độ Kernel (evdev qua `/dev/uinput`)**: loại bỏ gạch chân khó chịu, chống trượt/nuốt phím khi gõ tốc độ cao, và tương thích mượt mà với Chromium, Electron, JetBrains IDEs, Terminal và Game.

---

## ⚡ Điểm nổi bật

- **Chế độ Kernel (`/dev/uinput`)**: Giao tiếp trực tiếp tầng evdev với adaptive timing & echo debt guard — không trượt ký tự, không kẹt phím xóa.
- **Per-App Input Mode**: Gán chế độ gõ riêng cho từng ứng dụng, chuyển đổi tức thì bằng phím tắt (`Alt+Z`).
- **Tự động nhận diện Game**: Tự tắt bộ gõ khi phát hiện game (Steam, Proton, Wine, Lutris, Heroic...).
- **Kiểu gõ Telex**, bảng mã Unicode và TCVN3.
- **Gõ tắt (Macro) & Emoji**: Macro thông minh tự viết hoa (hot-reload không cần restart), tra cứu Emoji nhanh bằng `:tên_emoji`.
- **Kiểm tra chính tả**: Tùy chọn kiểm tra theo luật ghép vần hoặc từ điển tiếng Việt.
- **Giao diện GTK3**: Cài đặt trực quan, dễ quản lý phím tắt và danh sách app.

---

## 🎯 Chế độ gõ

| Chế độ       | ID  | Đặc điểm                                                                                                           |
| ------------ | --- | ------------------------------------------------------------------------------------------------------------------ |
| **Kernel**   | `0` | *(Mặc định)* Gõ qua evdev uinput, không gạch chân. Hoạt động mượt trên mọi app Wayland, Chromium, JetBrains, Game. |
| **Preedit**  | `1` | IBus preedit truyền thống (có gạch chân). Tự động chốt chữ khi mất focus.                                          |
| **Surround** | `2` | Dùng IBus SurroundingText cho các ứng dụng hỗ trợ.                                                                 |
| **Ignore**   | `7` | Tắt bộ gõ, passthrough phím gốc (thích hợp cho game và app đồ họa).                                                |

> Bấm **`Alt+Z`** tại cửa sổ bất kỳ để mở bảng chọn nhanh chế độ gõ cho ứng dụng đó.

---

## ⌨️ Phím tắt & Tiện ích

- **Chuyển Anh/Việt**: phím chuyển nguồn nhập của GNOME (`Super+Space`, hoặc `Caps Lock` nếu bật `grp:caps_toggle`). KTelex luôn để Caps Lock/phím đổi nhóm đi thẳng tới hệ thống, không bao giờ chặn.
- **Đổi chế độ gõ cho app**: `Alt+Z`. Phím tắt phải có Ctrl/Alt/Super (phím chữ trơn, Caps Lock, Esc, Backspace, Super+Space bị từ chối để không xung đột).
- **Macro (Gõ tắt)**: Cấu hình tại `~/.config/ibus-ktelex/ibus-ktelex.macro.text` theo dạng `vt:cụm từ`. Tự động giữ nguyên kiểu viết hoa (VD: `VT` → `CỤM TỪ`).
- **Emoji**: Gõ `:` kèm từ khóa tiếng Anh (VD: `:smile`, `:heart`) rồi bấm số hoặc Enter để chọn.

---

## 🛠️ Cài đặt & Sử dụng

### Yêu cầu

- Linux kernel hỗ trợ module `uinput`
- Go ≥ 1.23, IBus, GTK+ 3.0, libX11

### Cài đặt từ mã nguồn

```bash
git clone https://github.com/beheoxinh/kernel-telex.git
cd kernel-telex
./prepare.sh    # lần đầu: cài Go, gcc, GTK3, IBus dev… (dnf / apt / pacman)
./install.sh    # build ra dist/, cài vào /usr, cấp quyền /dev/uinput, khởi động lại IBus
```

Chạy `./install.sh` bằng user thường (script tự gọi `sudo` khi cần). Trên GNOME,
KTelex được thêm sẵn vào nguồn nhập: bấm `Super+Space` để chuyển. Lần cài đầu
tiên có thể cần đăng xuất/đăng nhập lại một lần để quyền nhóm `input` có hiệu lực.

Trên GNOME Wayland, `install.sh` cài thêm extension `wmclass-helper@local` để
KTelex biết cửa sổ đang focus (chế độ gõ theo app, nhận diện game). Extension
mới chỉ được nạp sau khi đăng xuất/đăng nhập lại.

Gỡ cài đặt: `./uninstall.sh` (thêm `--purge` để xoá cả cấu hình, macro, log).

### Lệnh phát triển

```bash
make build        # build ra dist/ibus-engine-ktelex
make test         # toàn bộ test suite, kèm race detector
make clean        # xoá dist/
```

`vendor/` được commit kèm repo (bamboo-core bản rút gọn, chỉ Telex), nên build
không cần tải module từ mạng.

*Log chẩn đoán: `~/.local/state/ktelex/ktelex.log` (tự xoay vòng ở 8 MB). Log chi
tiết từng phím (có thể chứa chữ đã gõ) chỉ bật khi đặt `KTELEX_DEBUG=1` hoặc tạo
file rỗng `~/.config/ibus-ktelex/debug`, rồi chạy `ibus restart`.*

---

## 📄 Giấy phép

Phát hành theo giấy phép [GPLv3](LICENSE).