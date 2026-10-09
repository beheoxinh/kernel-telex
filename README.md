<picture>
  <source media="(prefers-color-scheme: dark)" srcset="icons/vi-candy.svg">
  <img src="icons/vi.svg" alt="KTelex logo" width="80">
</picture>

# Kernel Telex (KTelex)

**Bộ gõ tiếng Việt hiệu năng cao cho Linux (GNOME / Wayland / X11) trên nền tảng IBus.**

KTelex tối ưu trải nghiệm gõ tiếng Việt trên Wayland bằng **chế độ Kernel (evdev qua `/dev/uinput`)**: loại bỏ gạch chân khó chịu, chống trượt/nuốt phím khi gõ tốc độ cao, và tương thích mượt mà với Chromium, Electron, JetBrains IDEs, Terminal và Game.

---

## ⚡ Điểm nổi bật

- **Chế độ Kernel (`/dev/uinput`)**: Giao tiếp trực tiếp tầng evdev với adaptive timing & echo debt guard — không trượt ký tự, không kẹt phím xóa.
- **Per-App Input Mode**: Gán chế độ gõ riêng cho từng ứng dụng, chuyển đổi tức thì bằng phím tắt (`Alt+Z`).
- **Tự động nhận diện Game**: Tự tắt bộ gõ khi phát hiện game (Steam, Proton, Wine, Lutris, Heroic...).
- **Đa dạng kiểu gõ & bảng mã**: Telex, VNI, VIQR; bảng mã Unicode, TCVN3, VNI Win, v.v.
- **Gõ tắt (Macro) & Emoji**: Macro thông minh tự viết hoa (hot-reload không cần restart), tra cứu Emoji nhanh bằng `:tên_emoji`.
- **Kiểm tra chính tả**: Tùy chọn kiểm tra theo luật ghép vần hoặc từ điển tiếng Việt.
- **Giao diện GTK3**: Cài đặt trực quan, dễ quản lý phím tắt và danh sách app.

---

## 🎯 Chế độ gõ

| Chế độ | ID | Đặc điểm |
|--------|----|----------|
| **Kernel** | `0` | *(Mặc định)* Gõ qua evdev uinput, không gạch chân. Hoạt động mượt trên mọi app Wayland, Chromium, JetBrains, Game. |
| **Preedit** | `1` | IBus preedit truyền thống (có gạch chân). Tự động chốt chữ khi mất focus. |
| **Surround** | `2` | Dùng IBus SurroundingText cho các ứng dụng hỗ trợ. |
| **Ignore** | `3` | Tắt bộ gõ, passthrough phím gốc (thích hợp cho game và app đồ họa). |

> Bấm **`Alt+Z`** tại cửa sổ bất kỳ để mở bảng chọn nhanh chế độ gõ cho ứng dụng đó.

---

## ⌨️ Phím tắt & Tiện ích

- **Đổi chế độ gõ cho app**: `Alt+Z`
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
make
sudo make install
ibus restart
```

### Lệnh phát triển

```bash
make test         # Chạy toàn bộ test suite (kèm race detector)
make clean        # Dọn dẹp binary và file tạm
```

*Log chẩn đoán: `/tmp/ktelex.log` (khi chạy engine IBus).*

---

## 📄 Giấy phép

Phát hành theo giấy phép [GPLv3](LICENSE).