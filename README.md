<picture>
  <source media="(prefers-color-scheme: dark)" srcset="icons/vi-candy.svg">
  <img src="icons/vi.svg" alt="KTelex logo" width="96">
</picture>

# Kernel Telex (KTelex)

**Bộ gõ tiếng Việt cho Linux Gnome — chạy trên IBus, gõ thẳng qua evdev (kernel).**

KTelex là một input method engine (IME) cho IBus framework trên Linux. Khác với các bộ gõ IBus truyền thống chỉ dùng preedit text, KTelex hỗ trợ **chế độ Kernel** — gửi tín hiệu bàn phím trực tiếp qua `/dev/uinput` (evdev), giúp gõ tiếng Việt mượt trong các ứng dụng Wayland, terminal, và các app không hỗ trợ preedit text.

---

## Tính năng

| Tính năng | Mô tả |
|-----------|-------|
| **4 chế độ gõ** | Kernel (uinput/evdev), Preedit, SurroundingText, Ignore (tắt) |
| **Chế độ Kernel** | Gõ tiếng Việt qua evdev — hoạt động trên mọi ứng dụng, kể cả game và Wayland |
| **Hỗ trợ game** | Tự động phát hiện Steam/Proton/Wine/Lutris/Heroic và tạm tắt bộ gõ |
| **Kiểu gõ** | Telex, VNI, VIQR, và tự định nghĩa |
| **Bảng mã** | Unicode, TCVN3, VNI Win, VIQR, và nhiều bảng mã khác |
| **Gõ tắt (Macro)** | Mở rộng từ viết tắt thành văn bản đầy đủ, tự động viết hoa, hot-reload |
| **Emoji** | Gõ `:` + tên emoji (VD: `:grin`) — tra cứu qua Trie, chọn số |
| **Kiểm tra chính tả** | Dùng luật ghép vần hoặc từ điển tiếng Việt |
| **Gán chế độ gõ theo app** | Mỗi ứng dụng có thể có chế độ gõ riêng (VD: game dùng Kernel, browser dùng Preedit) |
| **Phím tắt** | Custom shortcut cho chuyển chế độ gõ, tắt/mở tiếng Việt, emoji |
| **Đa màn hình desktop** | Hỗ trợ Wayland (wlr-foreign-toplevel), X11 (Xlib), GNOME Shell (D-Bus) |
| **EN engine** | Bộ gõ English (US/UK) với macro và game detection |
| **GUI cấu hình** | GTK3 — chỉnh kiểu gõ, macro, phím tắt, per-app mapping |
| **Dấu thanh tự do** | Bỏ dấu kiểu tự do hoặc chuẩn (òa vs oà) |

---

## Chế độ gõ

| Chế độ | Tên tắt | Cách hoạt động |
|--------|---------|----------------|
| **Kernel** | `UinputIM` | Gửi BackSpace evdev qua /dev/uinput → commit text. Hoạt động trên mọi app (kể cả Chromium Wayland, game). Không gạch chân preedit. |
| **Preedit** | `PreeditIM` | Dùng IBus preedit text (gạch chân). Phù hợp với app hỗ trợ IBus. |
| **Surround** | `SurroundingTextIM` | Dùng IBus SurroundingText + DeleteSurroundingText. |
| **Ignore** | `UsIM` | Tắt bộ gõ — forward hết phím xuống app. |

### Per-app Input Mode Mapping

Mỗi app có thể có chế độ gõ riêng. Nhấn phím tắt **Chuyển chế độ gõ** (mặc định: `Alt+z`) để mở bảng chọn nhanh chế độ gõ cho app hiện tại.

---

## Phím tắt (Shortcuts)

| Hành động | Mặc định |
|-----------|----------|
| Chuyển chế độ gõ | `Alt+z` |
| Khôi phục phím | — |
| Tạm tắt bộ gõ | — |
| Emoji | — |

Cấu hình qua GUI → *KTelex Settings* → tab Phím tắt.

---

## Kiểu gõ (Input Methods)

KTelex hỗ trợ các kiểu gõ mặc định:

- **Telex** `(aw → ă, aa → â, dd → đ, ow → ơ, uw → ư, z → sắc, ...)`
- **VNI** `(a8 → ă, a6 → â, d9 → đ, o7 → ơ, u7 → ư, 1 → sắc, ...)`
- **VIQR** `(aa → â, dd → đ, ee → ê, oo → ô, ` → huyền, ...)`
- Tự định nghĩa kiểu gõ riêng qua GUI

---

## Kiểm tra chính tả

Bật **Spell Check** trong menu Nâng cao → dùng:

1. **Rules** — luật ghép vần tiếng Việt (mặc định)
2. **Dictionary** — từ điển `vietnamese.cm.dict` (7.884 từ)

Khi bật spell check, KTelex tự động fallback sang English mode nếu chuỗi gõ không phải tiếng Việt hợp lệ.

---

## Macro (Gõ tắt)

Định nghĩa từ viết tắt trong file `~/.config/ibus-ktelex/ibus-ktelex.macro.text`.

Định dạng: `từ_viết_tắt:nội_dung_mở_rộng`

```
vn:việt nam
csao:✪
->:arrow
```

Tự động viết hoa nếu gõ in hoa (VD: `VN` → `VIỆT NAM`). File được hot-reload mỗi 3 giây.

---

## Emoji

Gõ `:` + từ khóa tiếng Anh → hiện lookup table emoji. Chọn bằng số hoặc Enter.

Dùng bộ dữ liệu [EmojiOne](https://www.emojione.com/) (~93.000 mục).

---

## Cài đặt

### Yêu cầu

- Go ≥ 1.23
- IBus
- GTK+ 3.0 (cho GUI)
- libX11, libXtst (cho X11 focus)
- Linux kernel với `CONFIG_INPUT_UINPUT` (cho chế độ Kernel)

### Từ source

```bash
git clone https://github.com/beheoxinh/kernel-telex
cd kernel-telex
make
sudo make install
ibus restart
```

### Nix

```bash
nix build
```

### Arch Linux

PKGBUILD có trong `build/arch/`.

### Debian/Ubuntu

```bash
make deb
```

### RPM

```bash
make rpm
```

---

## Kiến trúc

```
┌──────────────┐     ┌─────────────────────┐     ┌─────────────┐
│   Ứng dụng   │◄───►│     IBus Daemon     │◄───►│ ibus-engine-│
│  (GTK/Qt/WL) │     │  (D-Bus session)    │     │   ktelex    │
└──────────────┘     └─────────────────────┘     └──────┬──────┘
                                                         │
                                    ┌────────────────────┼────────────────────┐
                                    │                    │                    │
                               ┌────▼────┐        ┌──────▼───────┐    ┌─────▼──────┐
                               │ Preedit │        │ uinput-server │    │ Surrounding│
                               │   IM    │        │  (standalone) │    │  Text IM   │
                               └─────────┘        │  /dev/uinput  │    └────────────┘
                                                   └──────┬───────┘
                                                          │
                                                   ┌──────▼───────┐
                                                   │   evdev      │
                                                   │ (kernel)     │
                                                   └──────────────┘
```


## Build

```bash
make              # Build ibus-engine-ktelex + uinput-server
make test         # Chạy unit tests
sudo make install # Cài vào /usr
```

Debug log: `/tmp/ktelex.log` (khi chạy `--ibus`).

---

## Giấy phép

GPLv3. Xem [LICENSE](LICENSE).

---