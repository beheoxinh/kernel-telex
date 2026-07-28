# Kiến trúc Kernel Telex

> Tài liệu phân tích kiến trúc tổng thể, nghiệp vụ, luồng xử lý bàn phím và
> đánh giá tương thích ứng dụng cho bộ gõ tiếng Việt trên Linux.

**Tác giả:** BeHeoXinh
**Phiên bản:** 0.8.5
**Ngày:** 2026-07-29

---

## Mục lục

1. [Tổng quan hệ thống](#1-tổng-quan-hệ-thống)
2. [Kiến trúc component](#2-kiến-trúc-component)
3. [Mô hình luồng xử lý (Threading Model)](#3-mô-hình-luồng-xử-lý-threading-model)
4. [Luồng xử lý key event chi tiết](#4-luồng-xử-lý-key-event-chi-tiết)
5. [4 chế độ gõ (Input Modes)](#5-4-chế-độ-gõ-input-modes)
6. [Focus tracking & Per-app mapping](#6-focus-tracking--per-app-mapping)
7. [Kernel Mode (uinput/evdev) deep dive](#7-kernel-mode-uinputevdev-deep-dive)
8. [Macro Engine & Spell Check](#8-macro-engine--spell-check)
9. [Emoji Engine](#9-emoji-engine)
10. [GUI Configuration](#10-gui-configuration)
11. [Tương thích ứng dụng Linux](#11-tương-thích-ứng-dụng-linux)
12. [So sánh với các bộ gõ khác](#12-so-sánh-với-các-bộ-gõ-khác)
13. [Configuration & Startup](#13-configuration--startup)

---

## 1. Tổng quan hệ thống

Kernel Telex (KTelex) là một **IBus Engine** — phần mềm xử lý phương thức nhập
liệu (Input Method Engine — IME) cho Linux Desktop, chạy trên framework
[IBus](https://github.com/ibus/ibus) (Intelligent Input Bus). Nó có nhiệm vụ
chuyển đổi các tổ hợp phím ASCII tiếng Anh thành ký tự tiếng Việt có dấu.

### 1.1. Vị trí trong stack đồ họa Linux

```
┌─────────────────────────────────────────────────────────────────────┐
│                   Ứng dụng (Application)                           │
│  (GTK3/4, Qt5/6, Electron, Chromium, Terminal, Game, Wine)        │
└────────────────────────┬────────────────────────────────────────────┘
                         │ key events via Wayland / X11
                         ▼
┌─────────────────────────────────────────────────────────────────────┐
│              Compositor (Mutter/KWin/Sway/Hyprland)                │
└────────────────────────┬────────────────────────────────────────────┘
                         │ IBus D-Bus protocol
                         ▼
┌─────────────────────────────────────────────────────────────────────┐
│                        IBus Daemon                                  │
│                    (ibus-daemon — D-Bus session)                    │
│  ┌─────────────┐  ┌──────────────┐  ┌───────────────────────────┐  │
│  │ ibus-engine- │  │ ibus-engine- │  │  ibus-engine-ktelex      │  │
│  │ ktelex (VI)  │  │ ktelexus (EN)│  │   (in-process: this!)    │  │
│  └─────────────┘  └──────────────┘  └───────────────────────────┘  │
└────────────────────────┬────────────────────────────────────────────┘
                         │ BIOS: /dev/uinput (chế độ Kernel)
                         │ D-Bus (chế độ Preedit/Surround)
                         ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    Linux Kernel (evdev / input)                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 1.2. Ba binary của dự án

| Binary | Vai trò | Loại |
|--------|---------|------|
| `ibus-engine-ktelex` | IBus engine chính (VI + EN) + uinput device in-process | CGo |
| `ktelex` | Uinput server standalone | CGo |
| `ibus-setup-KTelex.desktop` | GTK3 GUI launcher | Desktop entry |

### 1.3. Các engine đăng ký với IBus

| Tên engine | IBus path | Layout | Ngôn ngữ |
|-----------|-----------|--------|----------|
| `KTelex` | `/org/freedesktop/IBus/Engine/KTelex/*` | default | vi |
| `KtelexUs` | `/org/freedesktop/IBus/Engine/ktelexus/*` | us | en_US |
| `KtelexGb` | `/org/freedesktop/IBus/Engine/ktelexgb/*` | gb | en_GB |

Cả 3 engine đều chạy trong cùng một process (ibus-engine-ktelex). EN engine là
pass-through (forward hết phím), chỉ có VI engine thực sự xử lý tiếng Việt.

---

## 2. Kiến trúc Component

### 2.1. Sơ đồ class chính

```
┌──────────────────────────────────────────────────────────────────────────┐
│                              main.go                                     │
│  - Flag parsing (--ibus, --version, --gui)                              │
│  - Wayland/GNOME detection                                              │
│  - uinputInitDirect()                                                    │
│  - GetIBusEngineCreator() → ibus.NewBus() → ibus.NewFactory()           │
│  - select {} (block forever)                                             │
└────────────────────┬─────────────────────────────────────────────────────┘
                     │ calls
                     ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                      engine_utils.go                                     │
│  GetIBusEngineCreator() → trả về factory function:                       │
│  1. config.LoadConfig(engineName)                                        │
│  2. bamboo.ParseInputMethod()                                           │
│  3. ibus.BaseEngine(conn, objectPath)                                   │
│  4. NewIbusBambooEngine(..., cfg, &baseEngine, bambooEngine)            │
│  5. engine.init()                                                        │
│  6. ibus.PublishEngine(conn, objectPath, engine)                        │
└────────────────────┬─────────────────────────────────────────────────────┘
                     │
     ┌───────────────┼───────────────────────┐
     ▼               ▼                       ▼
┌──────────┐  ┌──────────────────┐  ┌──────────────────┐
│ engine.go│  │ engine_preedit.go│  │engine_backspace.go│
│ Core     │  │ PreeditIM        │  │ SurroundingTextIM│
│ Engine   │  │ KernelIM (uinput)│  │                  │
│ struct   │  │                  │  │                  │
└──────────┘  └──────────────────┘  └──────────────────┘
     │               │                       │
     └───────────────┼───────────────────────┘
                     ▼
     ┌──────────────────────────────────────┐
     │         engine_utils.go              │
     │  shortcut handling                   │
     │  wmClass tracking                    │
     │  input mode lookup table             │
     │  notifications                       │
     └──────────────────────────────────────┘
     │               │                       │
     ▼               ▼                       ▼
┌──────────┐  ┌──────────┐  ┌──────────────────┐
│ emoji.go │  │ mactab.go│  │   trie.go        │
│ Emoji    │  │ Macro    │  │ Generic Trie     │
│ Engine   │  │ Table    │  │ (prefix search)  │
└──────────┘  └──────────┘  └──────────────────┘
```

### 2.2. Core engine struct

File: `engine.go` — struct `Engine` (dòng 39-80)

```go
type Engine struct {
    sync.Mutex
    IEngine                           // IBus engine interface (D-Bus methods)
    preeditor              bamboo.IEngine // bamboo-core engine (xử lý tiếng Việt)
    engineName             string
    config                 *config.Config
    propList               *ibus.PropList
    respectAppMapping      bool        // EN engine ignores per-app InputModeMapping
    englishMode            bool        // current mode (VI/EN)
    macroTable             *MacroTable
    wmClasses              string      // current focused app's WM_CLASS
    isInputModeLTOpened    bool        // input mode lookup table visible
    isEmojiLTOpened        bool        // emoji lookup table visible
    emojiLookupTable       *ibus.LookupTable
    inputModeLookupTable   *ibus.LookupTable
    capabilities           uint32
    keyPressDelay          int
    isFirstTimeSendingBS   bool
    // --- Uinput (Kernel mode) state ---
    uinputDeleting_         bool
    uinputExpectingBs_      int
    uinputSeenBs_           int
    uinputPendingCommit_    string
    uinputDeferredKeys_     []uint32
    uinputDeferredCodes_    []uint32
    uinputDeferredStates_   []uint32
    uinputCommitTimer_      *time.Timer
    uinputBsSentAt_         time.Time
    uinputBsRtEwmaUsec_     int64
    uinputPassthroughUntil_ time.Time
    uinputNoEcho_           bool
    uinputLastWm_           string
    // --- Support engines ---
    emoji                  *EmojiEngine
    isSurroundingTextReady bool
    lastKeyWithShift       bool
    lastCommitText         int64
    shouldRestoreKeyStrokes bool
    shouldEnqueuKeyStrokes bool
    isGame                 bool
}
```

### 2.3. Các subsystem chính

| Subsystem | File | Trách nhiệm |
|-----------|------|-------------|
| **IEngine interface** | `fake_engine.go` | IBus D-Bus method contract (47 methods) |
| **Bamboo-core** | vendor | Xử lý Telex/VNI/VIQR → Unicode |
| **IBus bridge** | vendor/goibus | IBus D-Bus binding |
| **Wayland focus** | `wl_introspector.go`, `client.go` | wlr-foreign-toplevel |
| **X11 focus** | `x11.go`, `x11_introspector.c` | Xlib _NET_ACTIVE_WINDOW |
| **GNOME focus** | `gnome_introspector.go` | GNOME Shell D-Bus Eval |
| **Uinput device** | `uinput_device.go` | /dev/uinput CGo |
| **Uinput client** | `uinput_client.go` | Unix socket ↔ uinput-server |
| **Uinput server** | `cmd/uinput-server/main.go` | Standalone /dev/uinput |
| **Game detection** | `isGame.go` | /proc/PID/comm + environ |
| **GTK3 GUI** | `ui/ui.go`, `ui/keyboard-shortcut-editor.c` | Cấu hình + shortcut editor |
| **Config** | `config/config.go` | JSON load/save + migration |
| **IBus constants** | `ibus_const.go` | Key codes, modifier masks |

---

## 3. Mô hình luồng xử lý (Threading Model)

### 3.1. Goroutines khi chạy `--ibus`

```
main goroutine
├── wlGetFocusWindowClass()       [NẾU Wayland && !Gnome]
│   └── goroutine * 1: Wayland event loop (wlc display.Dispatch)
│
├── uinputInitDirect()            [NẾU --ibus]
│   └── sync.Once: mở /dev/uinput (CGo)
│
├── keyPressCapturing()           [từ GetIBusEngineCreator]
│   └── goroutine * 1: for-range keyPressChan → keyPressHandler
│
├── ibus.NewFactory()
│   └── goroutine * N (dbus signal handler):
│       └── mỗi key event → engine.ProcessKeyEvent() trên dbus goroutine
│
├── engine.init()                 [cho mỗi engine instance]
│   └── macroTable.Enable() → goroutine * 1 (poll file 3s)
│
├── select {} (block main)
│
└── ibus.NewBus() internal goroutines:
    └── dbus.Conn goroutines (signal match, read/write)
```

### 3.2. Vấn đề đồng bộ (Concurrency concerns)

| Shared state | Loại | Đồng bộ? | Rủi ro |
|-------------|------|----------|--------|
| `keyPressHandler` (global func) | write-once (init), read-many | ❌ Không | Multiple engines overwrite nhau |
| `keyPressChan` | chan [3]uint32 | ✅ Thread-safe (channel) | Full buffer → deadlock (xem mục 4.3) |
| `wlAppId` (global string) | write (wl goroutine), read (FocusIn) | ❌ Không | Data race |
| `uinputFd` (global C.int) | sync.Once + Mutex | ✅ sync.Once + uinputDmu | OK |
| `dictionary` (global map) | Load trong FocusIn | ❌ Không | Data race nếu 2 engine cùng FocusIn |
| `emojiTrie` (global *TrieNode) | Load trong FocusIn | ❌ Không | Data race nếu 2 engine cùng FocusIn |
| Engine.uinput* fields | Mutex (Engine.Lock) | ✅ | OK (nhưng cần verify đồng nhất) |

---

## 4. Luồng xử lý key event chi tiết

### 4.1. Entry point: `ProcessKeyEvent()` — engine.go dòng 111

```
ProcessKeyEvent(keyVal, keyCode, state)
│
├─ Release key (state & IBusReleaseMask)?
│   └─ true → return (false, nil)  // không xử lý key release
│
├─ EN engine (ktelexus/ktelexgb)?
│   ├─ InputModeLT mở? → ltProcessKeyEvent()
│   ├─ Shortcut InputModeSwitch? → openLookupTableWithMode() → return true
│   └─ else → return (false, nil)  // pure passthrough
│
├─ processShortcutKey(keyVal, keyCode, state)
│   ├─ CapsLock? → return (true, false)
│   ├─ Emoji shortcut? → openEmojiList() → return (true, true)
│   ├─ EmojiLT đang mở? → emojiProcessKeyEvent()
│   ├─ InputModeLT đang mở? → ltProcessKeyEvent()
│   ├─ InputModeSwitch shortcut? → openLookupTable() → return (true, true)
│   ├─ RestoreKeystrokes shortcut? → set flag → return (false, false)
│   ├─ ViEnSwitch shortcut? → toggle englishMode → notify → return (true, true)
│   ├─ Shift L/R? → return (true, false)
│   ├─ UsIM? → return (true, false)
│   ├─ englishMode? → return (true, false)
│   └─ else → return (false, false)
│
├─ inBackspaceWhiteList()? (SurroundingTextIM)
│   └─ true → bsProcessKeyEvent()
│
├─ checkInputMode(UinputIM)?
│   └─ true → uinputProcessKeyEvent()
│
└─ else → preeditProcessKeyEvent()
```

### 4.2. PreeditIM flow: `preeditProcessKeyEvent()` — engine_preedit.go dòng 34

```
preeditProcessKeyEvent(keyVal, keyCode, state)
│
├─ CanProcessKey check when idle → return false
│
├─ BackSpace?
│   ├─ runeCount == 1? → commitPreeditAndReset("")
│   ├─ rawKeyLen > 0? → preeditor.RemoveLastChar → updatePreedit
│   └─ else → return false
│
├─ Tab?
│   ├─ Macro match? → commitPreeditAndReset(macroText)
│   └─ else → commitPreeditAndReset(composed) + return false
│
├─ getCommitText(keyVal, keyCode, state)
│   ├─ RestoreKeystrokes? → preeditor.RestoreLastWord()
│   ├─ CanProcessKey + AppendingKeys? → xử lý [[ → [ ...
│   ├─ CanProcessKey + macro enabled? → getProcessedString(PunctuationMode)
│   ├─ CanProcessKey normal? → preeditor.ProcessKey + getPreeditString()
│   ├─ Macro prefix matching? → append EnglishMode
│   ├─ Macro full match? → expandMacro + append keyS
│   └─ else → handleNonVnWord() → fallback English
│
├─ WordBreakSymbol?
│   ├─ commitPreeditAndResetForWBS(newText, isPrintable)
│   └─ return (isPrintable, nil)
│
├─ UinputIM? → return (isPrintable, nil) [không update preedit text]
│
└─ updatePreedit(newText)
    └─ return (isPrintable, nil)
```

### 4.3. SurroundingTextIM flow: `bsProcessKeyEvent()` — engine_backspace.go dòng 14

```
bsProcessKeyEvent(keyVal, keyCode, state)
│
├─ Movement key? → preeditor.Reset() → isSurroundingTextReady = true → return false
│
├─ First key (macro disabled, empty queue, no raw key)?
│   └─ CanProcessKey? → preeditor.ProcessKey(VietnameseMode)
│                        → bsCommitText() [commit ngay]
│                        → return true
│
├─ shouldEnqueuKeyStrokes?
│   ├─ SurroundingTextIM + BackSpace? → sleep() + removeLastChar → return false
│   ├─ SurroundingTextIM + Tab + no macro? → preeditor.Reset() → return false
│   ├─ SurroundingTextIM + invalid key? → sleep() → keyPressHandler → return
│   └─ Enqueue to keyPressChan → return true
│
└─ else → keyPressHandler(keyVal, keyCode, state)
```

**keyPressHandler (dòng 77)** — chạy trên goroutine keyPressCapturing:

```
keyPressHandler(keyVal, keyCode, state)
│
├─ BackSpace?
│   ├─ autoNonVnRestore? → removeLastChar → get offset
│   │   → offset != full text? → updatePreviousText()
│   └─ else → removeLastChar → return false
│
├─ Tab + macro? → updatePreviousText(old, macText)
│   ├─ else → return false
│
├─ getCommitText → get newText
│   ├─ shouldAppendDeadKey? → bsCommitText(" ") + SendBackSpace(1)
│   └─ updatePreviousTextInBatch(oldText, newText, isWordBreakRune)
│       ├─ SendBackSpace(n)
│       ├─ DRAIN keyPressChan: xử lý batch các key pending
│       │   → mỗi key: getCommitText → buffer
│       │   → word break? → batchCommit(old, buffer)
│       │   → invalid key? → ForwardKeyEvent (flush buffer trước)
│       └─ batchCommit hoặc bsCommitText
```

**⚠️ Vấn đề deadlock tiềm ẩn (SurroundingTextIM):**

`updatePreviousTextInBatch()` (dòng 160) drain `keyPressChan` bằng vòng lặp
`for i := 0; i < len(keyPressChan); i++` và `<-keyPressChan`. Khi channel no,
nó chỉ đọc đúng số phần tử hiện có (không block). Tuy nhiên:

1. `keyPressChan` là unbuffered với giới hạn 100 phần tử (make(chan [3]uint32, 100))
2. Nếu `bsProcessKeyEvent` enqueue nhanh hơn `keyPressHandler` xử lý, channel đầy
3. `bsProcessKeyEvent` block ở `keyPressChan <- ...`
4. Nhưng `keyPressHandler` (chạy trên keyPressCapturing goroutine) đang chờ ở
   `<-keyPressChan` để lấy phần tử tiếp theo
5. Tuy nhiên trong `updatePreviousTextInBatch`, nó drain channel bằng `<-keyPressChan`
   → đây là goroutine GIỐNG với keyPressCapturing → **block chính nó**?
   
Thực tế: `keyPressCapturing` goroutine gọi `keyPressHandler`, và 
`keyPressHandler` gọi `updatePreviousTextInBatch`, và nó drain `keyPressChan`
bằng `for i := 0; i < len(keyPressChan); i++`. `len(keyPressChan)` lấy số phần tử
hiện tại (non-blocking). Vòng lặp lấy đúng số phần tử đã thấy tại thời điểm bắt đầu.
Nếu trong lúc drain, có phần tử mới push vào, chúng KHÔNG được drain đợt này.

Đây KHÔNG phải deadlock, nhưng là **logic sai**: nó chỉ xử lý một snapshot của queue.
Các key đến sau snapshot sẽ được xử lý bởi lần gọi `keyPressHandler` tiếp theo
(lần gọi sau khi `bsProcessKeyEvent` enqueue thêm).

Nhưng có 1 vấn đề: `updatePreviousTextInBatch` dùng `<-keyPressChan` (receive).
`len(keyPressChan)` trả về số phần tử tại thời điểm đó. Vòng lặp chạy `i` từ 0 đến
`len-1`. Với mỗi iteration, `<-keyPressChan` lấy 1 phần tử. Nếu trong lúc này,
có phần tử mới đến, `len` không đổi → không lấy phần tử mới. Nhưng `len` được
tính 1 lần ở đầu vòng for. OK, không có lỗi blocking.

**Vấn đề thực sự**: nếu channel đầy (100 phần tử), `bsProcessKeyEvent` 
block ở `keyPressChan <- ...` trên **IBus D-Bus thread**. Trong khi đó
`keyPressCapturing` goroutine đang xử lý drain. Drain xong, nó gọi
`bsCommitText` (IBus CommitText D-Bus call) → cũng trên cùng D-Bus connection.
Nếu CommitText cần main loop iteration, và main loop bị block bởi
`bsProcessKeyEvent` (đang chờ push vào channel đầy) → **AB-BA deadlock** trên
D-Bus connection.

### 4.4. KernelIM (Uinput) flow — engine_preedit.go dòng 152

```
uinputProcessKeyEvent(keyVal, keyCode, state)
│
├─ Delete state (uinputDeleting_ == true)?
│   ├─ WATCHDOG: 300ms timeout → force commit + uinputNoEcho_ = true
│   ├─ uinputNoEcho_? → flush commit + forward key
│   ├─ BackSpace (BS echo)? → uinputSeenBs_++ → if done → timer commit
│   └─ other key? → buffer (tối đa 32 keys) + return true
│
├─ Ctrl/Alt+letter? → forward raw (return false)
├─ CanProcessKey when idle? → return false
├─ User BackSpace? → preeditor.RemoveLastChar → forward raw
├─ Tab + macro? → uinputBackspace(rawLen) + timer commit
├─ WordBreakSymbol? → resetPreedit → forward raw
├─ Non-printable (hết)? → resetPreedit → forward raw
│
├─ ProcessKey(VietnameseMode)
│   ├─ Simple append (old+keyS == afterText)? → forward raw
│   └─ Transformation?
│       ├─ noEcho mode? → ForwardKeyEvent BS + typeCodepoint
│       └─ Echo mode? → uinputBackspace(deleteLen) + timer commit
```

### 4.5. So sánh hành vi 3 chế độ gõ

| Khía cạnh | PreeditIM | SurroundingTextIM | KernelIM |
|-----------|-----------|-------------------|----------|
| **Cơ chế commit** | IBus preedit text | IBus CommitText + DeleteSurroundingText | uinput BS evdev + IBus CommitText |
| **Gạch chân** | ✅ Có | ❌ Không | ❌ Không |
| **Forward raw key** | Trả `false` (IBus forward) | Trả `false` | Trả `false` |
| **Key được consume** | Trả `true` | Trả `true` | Trả `true` |
| **Latency** | Thấp nhất | Cao (sleep 20-50ms) | Trung bình (EWMA adaptive) |
| **Xử lý BS** | preeditor.RemoveLastChar | preeditor + SendBackSpace | preeditor + ForwardKeyEvent BS |
| **Batch processing** | Không | Có (drain keyPressChan) | Có (buffer + replay) |
| **Phụ thuộc app** | App hỗ trợ IBus preedit | App hỗ trợ SurroundingText | App forward evdev BS qua IBus |
| **Thread** | IBus D-Bus goroutine | keyPressCapturing goroutine | IBus D-Bus goroutine |

---

## 5. 4 chế độ gõ (Input Modes)

### 5.1. Định nghĩa — config/flags.go

| Hằng số | Giá trị | Tên hiển thị | Mô tả |
|---------|---------|-------------|-------|
| `UinputIM` | 0 | Kernel | Gõ qua evdev, không preedit |
| `PreeditIM` | 1 | Preedit | Gõ qua IBus preedit text |
| `SurroundingTextIM` | 2 | Surround | Gõ qua SurroundingText API |
| `UsIM` | 7 | Ignore | Tắt bộ gõ, forward hết |

IMPORTANT: Các mode 3-6 (BackspaceForwardingIM, ShiftLeftForwardingIM,
ForwardAsCommitIM, XTestFakeKeyEventIM) đã bị loại bỏ. `migrateInputMode()`
ánh xạ các giá trị cũ này về `PreeditIM`.

### 5.2. Logic chọn mode — engine_utils.go dòng 262

```go
func (e *Engine) getInputMode() int {
    // 1. Per-app mapping (nếu respectAppMapping && wmClass != "")
    if e.respectAppMapping && e.getWmClass() != "" {
        if stored, ok := e.config.InputModeMapping[e.getWmClass()]; ok {
            im = migrateInputMode(stored)
            if _, ok := config.ImLookupTable[im]; ok {
                return im
            }
        }
    }
    // 2. Game detection
    if e.config.IBflags&config.IBdisableOnGame != 0 && e.isGame {
        return config.UsIM  // tắt bộ gõ trong game
    }
    // 3. Default
    im = migrateInputMode(e.config.DefaultInputMode)
    return im
}
```

### 5.3. Khi nào mode nào phù hợp?

| Loại ứng dụng | Kernel | Preedit | Surround | Ignore |
|--------------|--------|---------|----------|--------|
| **GTK3/4 apps** (Files, Gedit, Terminal) | ✅ Tốt | ✅ Tốt | ❌ Chậm | — |
| **Qt5/6 apps** (WPS Office, Telegram) | ✅ Tốt | ✅ Tốt | ⚠️ Có bug | — |
| **Electron apps** (VS Code, Slack, Discord) | ✅ Tốt | ✅ Tốt | ⚠️ Phụ thuộc | — |
| **Chromium/Chrome** (address bar) | ✅ Tốt | ⚠️ Cần workaround | ✅ | — |
| **Firefox** | ✅ Tốt | ⚠️ Cần workaround | ✅ | — |
| **GNOME Shell** (Overview search) | ❌ Ko cần | ✅ | ❌ | ✅ |
| **WPS Office** | ✅ Tốt | ✅ Tốt | ❌ | — |
| **Terminal** (GNOME Terminal, Alacritty) | ✅ Bắt buộc | ❌ Ko có preedit | ❌ | — |
| **Game (Steam/Proton/Wine)** | ✅ Bắt buộc | ❌ | ❌ | ✅ Auto |
| **FB Messenger (Web)** | ⚠️ Có thể | ✅ | ✅ | — |

---

## 6. Focus tracking & Per-app mapping

### 6.1. Ba nguồn focus tracking

```
getLatestWmClass()
│
├─ Gnome? → gnomeGetFocusWindowClass()
│   ├─ WMClassHelper D-Bus (extension) → nhanh
│   └─ org.gnome.Shell.Eval (GJS fallback) → chậm
│
├─ Wayland? → wlAppId (global, update từ goroutine)
│   └─ wlr-foreign-toplevel-management-unstable-v1
│
└─ else → x11GetFocusWindowClass() (CGo Xlib)
    └─ _NET_ACTIVE_WINDOW + XGetClassHint
```

### 6.2. Cơ chế hoạt động

```go
func (e *Engine) FocusIn() *dbus.Error {
    var latestWm = e.getLatestWmClass()
    e.checkWmClass(latestWm)          // reset buffer nếu app khác
    if e.config.IBflags&IBdisableOnGame != 0 {
        pid := getFocusedPID()
        e.isGame = isGameProcess(pid) // walk /proc
    }
    e.RegisterProperties(e.propList)
    e.RequireSurroundingText()
    // Load emojiTrie nếu chưa có
    if e.isShortcutKeyEnable(KSEmojiDialog) && emojiTrie == nil {
        emojiTrie, _ = loadEmojiOne(DictEmojiOne)
    }
    // Load dictionary nếu spell check bằng dict
    if e.config.IBflags&IBspellCheckWithDicts != 0 && len(dictionary) == 0 {
        dictionary, _ = loadDictionary(DictVietnameseCm)
    }
}
```

### 6.3. Per-app InputMode Mapping

- User nhấn shortcut → `openLookupTableWithMode(currentMode)`
- Hiển thị 4 mode: Kernel, Preedit, Surround, Ignore
- User chọn → `commitInputModeCandidate()`:
  1. Lưu `e.config.InputModeMapping[e.getWmClass()] = im`
  2. Lưu vào master config (VI) để EN engine dùng chung
  3. Update property list
- Mode này được áp dụng ngay lập tức (không cần restart)

---

## 7. Kernel Mode (uinput/evdev) deep dive

### 7.1. Tại sao cần chế độ Kernel?

**Vấn đề:** Trên Wayland, các ứng dụng không hỗ trợ preedit text (terminal,
game, Chromium) không thể gõ tiếng Việt vì IBus engine không có cách nào
để thay thế text đã gõ.

**Giải pháp:** Gửi tín hiệu BackSpace giả qua `/dev/uinput` (evdev) để xóa
ký tự cũ, sau đó commit ký tự mới (có dấu) qua IBus D-Bus. App nhìn thấy:
người dùng xóa `a` rồi gõ `á`.

### 7.2. Kiến trúc uinput

Hai đường dẫn đến evdev:

```
Đường A: In-process (CGo)
──────────
ibus-engine-ktelex
  └─ uinputDirectBackspace(n)
       └─ C.uio_key(fd, KEY_BACKSPACE, 1)   // press
       └─ C.usleep(5000)                     // 5ms
       └─ C.uio_key(fd, KEY_BACKSPACE, 0)   // release
       └─ C.usleep(1000)                     // 1ms

Đường B: Unix socket → standalone server
──────────
ibus-engine-ktelex
  └─ uinputBackspace(n)
       └─ net.Dial("unix", "@ktelex-uinput")
       └─ binary.Write(sock, count, textLen, text)
            → uinput-server
               └─ handlePacket()
                    ├─ Backspace() × count
                    ├─ typeUtf8(text)
                    │   └─ typeCodepoint(r) × len
                    │       └─ Ctrl+Shift+U + hex + Enter
                    └─ hoặc exec.Command("ydotool", "type", text)
```

### 7.3. Luồng biến đổi (Transformation)

Ví dụ gõ `duowidro` → `đuổi`:

```
1. Gõ 'd'     → old=""     after="d"     → append → forward 'd'
2. Gõ 'u'     → old="d"    after="du"    → append → forward 'u'
3. Gõ 'o'     → old="du"   after="duo"   → append → forward 'o'
4. Gõ 'w'     → old="duo"  after="dươ"   → TRANSFORM
               → prefix="d" del="uo" add="ươ"
               → uinputBackspace(2)       → BS BS
               → timer(EWMA delay)        → commitText("ươ")
5. Gõ 'i'     → old="dươ" after="dươi"   → append → forward 'i'
6. Gõ 'd'     → old="dươi" after="đươi"  → TRANSFORM
               → prefix="" del="dươi" add="đươi"
               → uinputBackspace(4)       → BS BS BS BS
               → timer(EWMA delay)        → commitText("đươi")
7. Gõ 'r'     → old="đươi" after="đưởi"  → TRANSFORM
               → prefix="đư" del="ơi" add="ởi"
               → uinputBackspace(2)
               → timer(EWMA delay)
8. Gõ 'o'     → old="đưởi" after="đuổi"  → TRANSFORM
               → prefix="đ" del="ưởi" add="uổi"
               → uinputBackspace(3)
               → timer(EWMA delay)
```

### 7.4. Echo counting & Adaptive delay

**Cơ chế:** Trên Wayland, tín hiệu evdev BS loop back qua IBus thành
`ProcessKeyEvent(BackSpace)` — gọi là "BS echo". Engine đếm BS echoes
để biết khi nào app đã xóa xong ký tự cũ, sau đó mới commit ký tự mới.

**EWMA (Exponentially Weighted Moving Average):**
```go
ewma = α * rt + (1 - α) * ewma   // α = 0.5
delay = max(ewma, 10ms)
```

**Watchdog timeout (300ms):**
Nếu app không echo BS (vd: Chromium Wayland), watchdog force-commit:
```
uinputNoEcho_ = true
→ Chuyển sang ForwardKeyEvent BS (IBus D-Bus) thay vì chờ echo
→ Cho đến lần FocusOut tiếp theo
```

**Vấn đề với uinputNoEcho_:**
- Một khi chuyển sang no-echo mode, nó ở đó vĩnh viễn (đến FocusOut)
- App có thể echo ở field này nhưng không echo ở field khác
- → stuck ở no-echo mode

### 7.5. Passthrough protection

```go
// Sau khi gửi typeCodepoint (Ctrl+Shift+U...), uinput tạo ra key events
// loop về IBus. Engine cần ignore các key này để không xử lý lại.
typeCodepoint:
    C.uio_key(fd, KEY_LEFTCTRL, 1)
    C.uio_key(fd, KEY_LEFTSHIFT, 1)
    ...
    C.uio_key(fd, KEY_ENTER, 1)
    // uinputPassthroughUntil_ = now + delay
```

`uinputPassthroughUntil_` được set trong `uinputBsFixedDelay()` nhưng
**không được check trong `uinputProcessKeyEvent()`** — nó không được dùng!
Dead code → passthrough protection không hoạt động.

### 7.6. Fixed delay mode

```go
func (e *Engine) uinputBsFixedDelay(bsCount int, commitText string) {
    uinputBackspace(bsCount)
    // Tính thời gian passthrough:
    addLen := utf8.RuneCountInString(commitText)
    ms := time.Duration(bsCount + 3 + addLen*12 + 10) * time.Millisecond
    e.uinputPassthroughUntil_ = time.Now().Add(ms) // ⚠️ không được dùng
}
```

---

## 8. Macro Engine & Spell Check

### 8.1. MacroTable — mactab.go

```
MacroTable
├─ mTable: map[string]string  (VD: "vn" → "việt nam")
├─ LoadFromFile(filename)
│   └─ Định dạng: tắt:mở_rộng  (VD: vn:việt nam)
│   └─ Comment: # hoặc ;
│   └─ Tự động lower key nếu autoCapitalizeMacro
│
├─ Enable(engineName)
│   └─ Goroutine: poll file mỗi 3s → hot-reload
│
└─ Disable()
    └─ Clear mTable
```

### 8.2. Macro matching

Khi gõ phím, engine kiểm tra:
1. `macroTable.HasPrefix(oldText + keyS)` — tiền tố → append EnglishMode
2. `macroTable.HasKey(oldText)` — match đúng → expandMacro + append keyS
3. Tự động viết hoa: `vn` → `việt nam`, `VN` → `VIỆT NAM`
4. Trigger: Space, Enter, Tab, dấu câu

### 8.3. Spell Check — engine_preedit.go dòng 483

Hai cơ chế kiểm tra chính tả:

**1. Rules-based (luật ghép vần):**
```go
func mustFallbackToEnglish() bool {
    return !e.preeditor.IsValid(true)
}
```
`bamboo-core` có luật ghép vần tiếng Việt. Nếu chuỗi không phải vần hợp lệ,
engine tự động fallback về English.

**2. Dictionary-based (từ điển):**
```go
if e.config.IBflags&IBspellCheckWithDicts != 0 {
    return !dictionary[vnSeq]  // 7.884 từ trong vietnamese.cm.dict
}
```

---

## 9. Emoji Engine

### 9.1. Trie-based search — trie.go

```
TrieNode
├─ isWord: bool
├─ value: string (":"-separated code points)
└─ Children: map[rune]*TrieNode

InsertTrie(root, "grin", "😀")
InsertTrie(root, "grinning", "😀")
InsertTrie(root, ":')", "😂")

FindPrefix(root, ":g") → {"grin": "😀", "grinning": "😀"}
FindPrefix(root, "grin") → {"grin": "😀", "grinning": "😀"}
```

### 9.2. Emoji dataset

- File: `data/emojione.json` (~93.426 dòng)
- Format: `{"codePoint": {"name":"...", "shortname":"...", "keywords":[...], "ascii":[...]}}`
- Load vào Trie lúc runtime (trong FocusIn)

### 9.3. UX

1. Gõ `:` → mở emoji lookup table
2. Gõ tiếp ký tự a-z → filter emoji (MatchString/Filter)
3. Hiển thị lookup table ngang (9 items/page)
4. Chọn bằng số (1-9) hoặc Enter
5. Escape → cancel

---

## 10. GUI Configuration

### 10.1. GTK3 GUI — ui/ui.go + ui/keyboard-shortcut-editor.c

```
OpenGUI(engineName, version, buildId)
├─ Load master config (VI engine)
├─ Load macro file
├─ Load per-app mappings
├─ Gọi C.openGUI() → GTK3 window
│   ├─ Tab 1: Kiểu gõ + Bảng mã
│   ├─ Tab 2: Macro editor
│   ├─ Tab 3: Phím tắt (shortcut recorder)
│   └─ Tab 4: Per-app input mode
│
└─ Callback C → Go:
    ├─ saveFlags(guint)            → toggle IBflags bit
    ├─ saveConfigText(char*)       → write config JSON
    ├─ saveMacroText(char*)        → write macro file
    ├─ saveShortcuts(guint32*, int)→ write shortcut array
    ├─ saveAppMode(char*, int)     → write per-app mapping
    ├─ removeAppMode(char*)        → delete per-app mapping
    ├─ saveInputMethodName(char*)  → write input method name
    ├─ saveOutputCharset(char*)    → write charset
    ├─ saveIBFlag(guint, int)      → set/clear IBflags bit
    └─ saveCoreFlag(guint, int)    → set/clear bamboo-core flag
```

### 10.2. Config JSON format

File: `~/.config/ibus-ktelex/ibus-ktelex.config.json`

```json
{
  "inputMethod": "Telex",
  "outputCharset": "Unicode",
  "freeToneMarking": false,
  "stdToneStyle": true,
  "autoCorrect": true,
  "useMacro": false,
  "autoCapitalizeMacro": true,
  "spellCheck": true,
  "spellCheckByRules": true,
  "spellCheckByDicts": false,
  "autoNonVnRestore": true,
  "ddFreeStyle": true,
  "disableOnGame": true,
  "defaultInputMode": 0,
  "inputModeMapping": {
    "Navigator:Firefox": 1,
    "google-chrome:Google-chrome": 2
  }
}
```

### 10.3. Shortcut encoding

Cấu trúc: `[10]uint32` (dạng raw → JSON serialize thành human-readable string)

| Index | Shortcut | Default |
|-------|----------|---------|
| 0-1 | InputModeSwitch | Alt+z (mask=8, keyval=122) |
| 2-3 | RestoreKeystrokes | — |
| 4-5 | ViEnSwitch | — |
| 6-7 | EmojiDialog | — |
| 8-9 | (reserved) | — |

---

## 11. Tương thích ứng dụng Linux

### 11.1. Ma trận tương thích chi tiết

| Ứng dụng | DE | Preedit | Surround | Kernel | Ghi chú |
|---------|-----|---------|----------|--------|---------|
| **GNOME Terminal** | GNOME/WL | ❌ | ❌ | ✅ | Terminal ko có preedit |
| **GNOME Terminal** | GNOME/X11 | ❌ | ❌ | ✅ | |
| **Alacritty** | any | ❌ | ❌ | ✅ | No preedit |
| **Kitty** | any | ❌ | ❌ | ✅ | No preedit |
| **Nautilus (Files)** | GNOME/WL | ✅ | ❌ | ✅ | |
| **Gedit** | GNOME | ✅ | ❌ | ✅ | |
| **GTK3 apps** | any | ✅ | ⚠️ | ✅ | GTK3 hỗ trợ IBus tốt |
| **Qt5/Qt6 apps** | any | ✅ | ⚠️ | ✅ | Qt IBus bridge |
| **Chromium (address bar)** | any/WL | ⚠️ workaround | ✅ | ✅ | has workaround |
| **Chromium (text area)** | any | ✅ | ✅ | ✅ | |
| **Chrome (address bar)** | any/X11 | ✅ | ✅ | ✅ | |
| **Firefox** | any | ⚠️ workaround | ✅ | ✅ | similar workaround |
| **VS Code** | any | ✅ | ✅ | ✅ | Electron IBus |
| **Slack/Discord** | any | ✅ | ✅ | ✅ | Electron |
| **WPS Office** | any/X11 | ✅ | ❌ | ✅ | Qt, has dedicated workaround |
| **LibreOffice** | any | ✅ | ❌ | ✅ | GTK3 |
| **Telegram Desktop** | any | ✅ | ❌ | ✅ | Qt |
| **Steam (client)** | any | ❌ | ❌ | ✅ | Chrome Embedded |
| **Steam Game (Proton)** | any | ❌ | ❌ | ✅ Auto | Game detection tắt engine |
| **Wine/Games** | any | ❌ | ❌ | ✅ Auto | Game detection |
| **WPS Office** | any/X11 | ✅ | ❌ | ✅ | enabledAuxiliaryTextList |
| **FB Messenger (web)** | any | ✅ workaround | ✅ | ⚠️ | commit trước hide preedit |
| **GNOME Shell Overview** | GNOME/WL | ✅ | ❌ | ❌ | return false ngay |

### 11.2. Workarounds đặc biệt

**Chromium/Firefox address bar — `shouldAppendDeadKey` (engine_backspace.go dòng 139):**
```go
func (e *Engine) shouldAppendDeadKey(newText, oldText string) bool {
    if e.isFirstTimeSendingBS && offset < len(newRunes) 
       && offset < len(oldRunes) && e.inBrowserList() {
        return true
    }
}
```
Gửi space + BackSpace trước khi update text — bug của browser address bar.

**WPS Office — `enabledAuxiliaryTextList`:**
Dùng AuxiliaryText thay vì PreeditText — WPS không render underline.

**FB Messenger — `commitPreeditAndResetForWBS`:**
Commit text TRƯỚC khi hide preedit (thay vì sau).

**Game detection — `isGame.go`:**
Walk /proc/PID chain để phát hiện Steam/Proton/Wine/Lutris/Heroic.
Check environment variables: SteamAppId, WINEPREFIX, STEAM_COMPAT_DATA_PATH.

### 11.3. Hạn chế đã biết

| Hạn chế | Mức độ | Mô tả |
|---------|--------|-------|
| Chromium Wayland echo BS | ⚠️ | Không forward evdev BS → stuck ở no-echo mode |
| GNOME Overview | ⚠️ | Không có wmClass → không detect được |
| Thread-safety globals | 🔴 | `keyPressHandler`, `dictionary`, `emojiTrie`, `wlAppId` |
| Deadlock SurroundingTextIM | 🔴 | D-Bus channel full + drain cùng goroutine |
| uinputPassthroughUntil_ unused | 🟡 | Dead code |
| usleep blocks CGo thread | 🟡 | Gây starvation Go thread pool |
| Multiple engines (VI+EN) | 🔴 | Global state bị ghi đè |
| Hardcoded DataDir (/usr/share/ktelex) | 🟡 | os.Chdir ảnh hưởng toàn process |

---

## 12. So sánh với các bộ gõ khác

| Tính năng | KTelex | ibus-bamboo | ibus-unikey | fcitx5-unikey |
|-----------|--------|------------|-------------|---------------|
| **Kernel (evdev) mode** | ✅ | ❌ | ❌ | ❌ |
| **IBus + Wayland** | ✅ | ✅ | ✅ | ❌ (fcitx5) |
| **Preedit** | ✅ | ✅ | ✅ | ✅ |
| **SurroundingText** | ✅ | ✅ | ❌ | ❌ |
| **Game detection** | ✅ | ❌ | ❌ | ❌ |
| **Per-app input mode** | ✅ | ✅ | ❌ | ❌ |
| **Emoji** | ✅ (Trie) | ✅ | ❌ | ❌ |
| **Macro** | ✅ (hot-reload) | ✅ | ❌ | ❌ |
| **Spell check** | ✅ (rules+dict) | ✅ | ❌ | ❌ |
| **Standalone uinput server** | ✅ | ❌ | ❌ | ❌ |
| **GTK3 GUI** | ✅ | ✅ | ✅ | ❌ (Qt) |
| **Kiểu gõ tự định nghĩa** | ✅ | ✅ | ❌ | ❌ |

---

## 13. Configuration & Startup

### 13.1. File paths

| File | Mục đích |
|------|---------|
| `~/.config/ibus-ktelex/ibus-{engine}.config.json` | Config chính |
| `~/.config/ibus-ktelex/ibus-{engine}.macro.text` | Macro table |
| `/usr/share/ktelex/data/emojione.json` | Emoji dataset |
| `/usr/share/ktelex/data/vietnamese.cm.dict` | Từ điển VN |
| `/usr/share/ktelex/data/ktelex.xml` | IBus component registration |
| `/usr/share/ktelex/icons/vi.svg` (us.svg) | Flag icons |
| `/tmp/ktelex.log` | Debug log (khi --ibus) |

### 13.2. Startup sequence

```
1. System boot → display manager → gnome-session
2. gnome-session → start ibus-daemon
3. ibus-daemon → đọc /usr/share/ibus/component/ktelex.xml
4. ibus-daemon → exec ibus-engine-ktelex --ibus
5. main.go:
   a. os.Chdir(DataDir) → /usr/share/ktelex
   b. isWayland = true (có WAYLAND_DISPLAY)
   c. hasGnome → true
   d. go wlGetFocusWindowClass() → Wayland event loop
   e. go uinputInitDirect() → mở /dev/uinput
   f. GetIBusEngineCreator()
      → keyPressCapturing() goroutine
      → trả về factory function
   g. ibus.NewBus() → kết nối D-Bus session
   h. bus.RequestName("org.freedesktop.IBus.ktelex")
   i. ibus.NewFactory(conn, engine)
      → IBus gọi factory cho mỗi engine đã đăng ký (KTelex, KtelexUs, KtelexGb)
      → factory: LoadConfig → NewEngine → NewIbusBambooEngine → init() → PublishEngine
   j. select {} → block vĩnh viễn
```

### 13.3. Lifecycle của một engine instance

```
Factory được gọi
├─ config.LoadConfig(engineName)
├─ bamboo.ParseInputMethod()
├─ ibus.BaseEngine(conn, objectPath)
├─ NewIbusBambooEngine(..., cfg, ..., bambooEngine)
├─ engine.init()
│   ├─ initConfigFiles() → tạo thư mục config + macro mẫu
│   ├─ NewEmojiEngine()
│   ├─ NewMacroTable()
│   ├─ Enable macro nếu cần
│   └─ keyPressHandler = e.keyPressForwardHandler
├─ ibus.PublishEngine(conn, objectPath, engine)
│   └─ → IBus gọi Enable(), FocusIn()
│
├─ [runtime] IBus gọi:
│   ├─ Enable()  → preeditor.Reset() + RequireSurroundingText()
│   ├─ Disable() → clear uinput state + preeditor.Reset()
│   ├─ FocusIn() → update wmClass, game detection, load emoji/dict
│   ├─ FocusOut() → reset preedit + clear uinput state
│   ├─ ProcessKeyEvent() → xử lý phím
│   └─ PropertyActivate() → thay đổi cấu hình
│
└─ [khi engine bị remove] → Destroy()
```

---

## Phụ lục: Bamboo-core (vendor)

### bamboo.IEngine interface

```go
type IEngine interface {
    CanProcessKey(rune) bool
    ProcessKey(rune, Mode)
    RemoveLastChar(bool)
    GetProcessedString(Mode) string
    GetInputMethod() InputMethodDefinition
    IsValid(bool) bool
    Reset()
    RestoreLastWord(bool)
}
```

### Các Mode (bitfield)

| Mode | Value | Effect |
|------|-------|--------|
| `VietnameseMode` | 0 | Xử lý tiếng Việt |
| `EnglishMode` | 1 | Bỏ qua dấu |
| `FullText` | 2 | Return full text |
| `LowerCase` | 4 | Lowercase |
| `PunctuationMode` | 8 | Include punctuation |
| `InReverseOrder` | 16 | Reverse order (surrounding text) |
| `EstdToneStyle` | 32 | òa style |
| `EfreeToneMarking` | 64 | Free tone marking |
| `EautoCorrectEnabled` | 128 | Auto correct |

### Input Method Definitions

Được nạp từ `bamboo.GetInputMethodDefinitions()` — trả về map các kiểu gõ:
- Telex, VNI, VIQR
- Tự định nghĩa qua GUI → override config.json
