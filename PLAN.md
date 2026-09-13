# SSHDESK Go 重写开发计划

> 原始项目：`/Users/rarnu/Code/github/sshdesk`（Python 3.10+，v0.4.5）
> 目标：用 Go 重写，功能完全一致。SSHDESK 是一个通过单条 SSH PTY 会话交付的完整交互式远程桌面——OpenSSH 负责认证/加密/传输，SSHDESK 作为 forced command 运行，把桌面画面渲染进终端（Kitty 图形协议像素 tile 或 ANSI 半块单元格），并把键盘/鼠标事件注入远程桌面。

---

## 第一部分：原项目功能点清单

### 1. 入口点与 forced-command 路由

9 个命令入口（Go 版合并为单二进制多命令，安装时以同名符号链接分发）：

| 命令 | 功能 |
|---|---|
| `sshdesk` / `sshdesk-server` | 交互式桌面会话服务端（PTY 内运行） |
| `sshdesk-local` | 本地一次性/循环渲染（开发用），`--once --columns --rows` |
| `sshdesk-forced-command` | sshd ForceCommand 调度器 |
| `sshdesk-agent` | 本地 agent 命令集（info/screenshot/move/click/scroll/type/key/session） |
| `sshdesk-agent-ssh` | 远程命令 allowlist 包装器（退出码 126 拒绝） |
| `sshdesk-remote` | 客户端：通过 `ssh <target> sshdesk-agent session` 发 NDJSON 请求 |
| `sshdesk-split` | 客户端：tmux 左右/上下分屏，右窗格桌面 + 左窗格 shell |
| `sshdesk-bench` | 合成帧渲染带宽基准 |

**forced-command 路由规则**（读 `SSH_ORIGINAL_COMMAND`）：
- 无命令 / `desktop` / `sshdesk` / `sshdesk-server` → 要求 PTY，启动桌面会话；`RUN_AS != 认证账户` 时 `sudo -n -u RUN_AS` 提权
- `shell` / `sshdesk-shell` → 要求 PTY，exec 认证账户自己的登录 shell（`-l`），**绝不**走 RUN_AS/sudo
- `sshdesk-agent ...` 或其他任何命令 → `sshdesk-agent-ssh`（严格解析，basename 必须为 `sshdesk-agent`，禁 `--output`，命令 ≤65536 字节，违反退出 2/126）

**退出码语义**：0 正常；1 错误；2 参数/协议违规；126 allowlist 拒绝；130 Ctrl+C 中断。

**`sshdesk-server` 参数**：`--capture {auto,x11,gnome,wayland,native,synthetic}`、`--input {auto,x11,mutter,ydotool,quartz,sendinput,none}`、`--display`、`--no-input`、`--synthetic-static`、`--color {auto,truecolor,256,16}`、`--no-mouse`、`--ascii`、`--max-fps 1-120`、`--scale 0.25-1.0`、`--check`（验证后端可用性后退出 0）。

### 2. 屏幕采集（capture）

帧契约：RGB24 像素 + `blake2s(像素, digest_size=8)` 内容摘要 + 桌面逻辑坐标系（图像尺寸可与桌面尺寸分离，预缩放采集仍保留桌面坐标供输入映射）。接口：`capture()/size()/cursor_position()/set_target_size(1-16384)/set_frame_rate(0.5-120)/close()`。

| 后端 | 机制 |
|---|---|
| X11 组合 | `SSHDESK_X11_CAPTURE` ∈ {auto,ffmpeg,xshm,pillow}；auto 依次 ffmpeg → xshm → xcb，OSError 永久降级；几何变化每秒轮询并重建 |
| FFmpeg/XCB | 子进程 `ffmpeg -f x11grab -draw_mouse 0 -framerate N -video_size WxH -vf scale=w:h:flags=bicubic -pix_fmt rgb24 -f rawvideo pipe:1`，stdout 持续读满一帧；stderr 守护线程排空（保留尾部 ≤2048 字节报错用） |
| MIT-SHM | libX11/libXext shm：XShmCreateImage(ZPixmap, 32bpp BGRX 掩码校验) + shmget/shmat + 立即 IPC_RMID；缩放 OpenCV 或 Pillow LANCZOS |
| XCB 兜底 | Pillow ImageGrab（内部 XCB）+ BICUBIC 缩放 |
| GNOME Wayland | Mutter D-Bus（`org.gnome.Mutter.DisplayConfig.GetCurrentState` 算多显示器并集 → RemoteDesktop.CreateSession → ScreenCast.CreateSession → RecordArea(cursor-mode=0 隐藏光标) → PipeWireStreamAdded 信号取 node → Start）；GStreamer 管线 `pipewiresrc ! queue leaky=downstream max-size-buffers=1 ! videoconvert ! videoscale ! video/x-raw,format=RGB ! appsink max-buffers=1 drop=true`；同一 D-Bus 会话联动 Mutter 输入 |
| Wayland（非 GNOME） | 截图命令选择顺序：GNOME+gnome-screenshot → KDE+spectacle → grim → 任意 gnome-screenshot → 任意 spectacle；`grim -c -t png -` 读 stdout；5s 超时；BICUBIC 缩放 |
| macOS native | Quartz `CGDisplayCreateImage(CGMainDisplayID())`，BGRA→RGB，Retina 按逻辑像素缩放；无权限返回 None → 报 Screen Recording 权限错误 |
| Windows native | `ImageGrab.grab(all_screens=True)` 等价（Go 用 BitBlt 截图） |
| synthetic | 确定性合成帧 1280×720：底色 (18,22,30)、顶栏 (35,42,55) "SSHDESK"、窗口 (48,57,73)、蓝色 (43,108,176) "Terminal desktop" 子窗口、animate 时橙色 (238,127,74) 圆以 frame*13 弹跳；无 digest |

### 3. 终端探测与能力回退

**环境变量推断**（capabilities，不发查询）：`TERM` 空/dumb/unknown 报错；颜色 `SSHDESK_COLOR` 覆盖 → `COLORTERM=truecolor/24bit` 或 TERM 含 direct/kitty/wezterm/alacritty → truecolor → TERM 含 256color → 256 → 16；`SSHDESK_MOUSE`/`SSHDESK_UNICODE` 接受 auto/0/1/false/true/no/yes；SGR 鼠标需 TERM 含 xterm/screen/tmux/rxvt/kitty/wezterm/alacritty（unicode 另含 linux）。

**图形探测**（probe，0.75s 超时，≤16KB，DA1 `ESC[c` 为终止哨兵）：
- Kitty 查询 `ESC_Gi=1893,s=1,v=1,a=q,t=d,f=24;AAAA ESC\`（id=0x765，响应含 `i=1893` + OK）
- `CSI 14t`（文本区像素，响应 `CSI 4;h;w t`）、`CSI 16t`（cell 像素，`CSI 6;h;w t`）
- `CSI ?1016$p`（SGR 像素鼠标）、`CSI ?2026$p`（同步输出）
- 像素几何优先 ioctl TIOCGWINSZ；`usable` = kitty 且四个像素几何值齐全
- tmux 内所有序列用 `ESC Ptmux;...(ESC 双写)...ESC\` 包裹

### 4. 渲染

**Kitty 像素渲染器**（sharp，60fps 目标）：
- 布局：顶栏 margin=1；`display_scale=min(usable_w/dw, usable_h/dh)` 保纵横比可放大；传输像素 `content_scale=min(1,display_scale)*render_scale`（永不上采样，放大靠终端 c/r cell-span）
- tile：目标 160px（clamp 80-160），换算 cell 整数倍；tile id = `0x7600 + tile_y*1024 + tile_x`；全幅双缓冲 id `0x7500/0x7501`；z 层级 full=-2 / tile=-1 / cursor=+1
- diff：ImageChops 差异包围盒 → 逐 tile 精判 → 变化像素 ≥15%（FULL_REPLACE_THRESHOLD）升级全幅替换
- tile 编码：RGB → 128 色 FASTOCTREE 量化（无抖动）→ PNG compress_level=1 → base64 → 4096 字节分块
- 放置序列：先删旧 placement（`a=d,d=i,i=ID,p=1`）→ 光标定位 → `a=T,q=1,C=1,z=-1,f=100,i=ID,p=1,c=CC,r=CR,m={0|1}`
- 全幅替换：`?2026h`…`?2026l` 同步包裹（若支持）→ 放新全幅（z=-2）→ `a=d,d=Z,z=-1` 清 tile 层 → `a=d,d=I,i=旧id` 删旧全幅
- ≥4 tile 时并行编码
- 光标：程序生成 9×13 RGBA 箭头（白填充黑描边），`f=32` 内联上传一次，之后 `a=p` 重放置（X/Y cell 内像素偏移）

**ANSI 单元格渲染器**（30fps 目标）：
- viewport：`scale=min(cols/dw, content_rows*2/dh)*render_scale`（一 cell = 垂直 2 源像素），居中 letterbox，顶栏 margin=1
- cell：fg=上半像素色，bg=下半像素色；glyph `▀`（U+2580）；ASCII 模式 glyph=空格且 fg/bg 取平均
- 颜色量化：truecolor `38;2;r;g;b`；256 色 6×6×6 cube vs 灰度平方距离（cube 值 `0 or 55+v*40`，gray=8+level*10）；16 色固定 xterm 调色板最近邻（SGR 30-37/40-47/90-97/100-107）
- diff：逐 cell 比较，变化 ≥60% 升级 FULL；delta 仅变化 cell，不连续才发 `CSI r;cH`，SGR run 压缩

**公共输出**：进入序列 = 标题压栈 `CSI 22;0t` + `ESC]2;SSHDESK - <hostname>ESC\` + `?1049h` + `?25l` + `2J H` + 鼠标 `?1003h ?1006h`（Kitty 加 `?7l ?1016h`）；离开序列反向 + `CSI 23;0t` 恢复标题（Kitty 先 `a=d,d=A` 清全部图像）。顶栏：`SSH DESK | <hostname>` 居中，fg (235,240,248) / bg (28,38,52)。RTT 探测：每秒 `CSI 6n`，应答 `CSI r;cR` 计算延迟。

### 5. 终端输入解析（parser 状态机）

- 缓冲上限 8192 字节；裸 ESC 用 35ms ESCAPE_DELAY 消歧
- `0x1D`（Ctrl+]）双击 → 退出（单击降级为 Ctrl+']'）；`0x13`（Ctrl+S）→ 切换统计叠加
- 控制字节：LF/CR→ENTER，BS/DEL→BACKSPACE，TAB，`0x01-0x1A`→Ctrl+a..z
- SGR 鼠标 `ESC[<b;c;r M/m`：b&64 滚轮（b&1 下=-1）、b&32 移动、按钮 (b&3)+1（1左/2中/3右）、修饰位忽略
- X10 legacy `ESC[M`+3 字节：release 由 code==3 表示，需记忆按下按钮
- 按键序列：CSI `1;<mod>X`、CSI `<n>;<mod>~`（1=Home,2=Ins,3=Del,4=End,5=PgUp,6=PgDn,15=F5,17-24=F6-F12），修饰 = (mod-1) bit0 SHIFT/bit1 ALT/bit2 CTRL；SS3 `ESC O P-S`=F1-F4 等
- Alt+字符 = ESC + UTF-8 字符；普通字符 UTF-8 解码（非法→U+FFFD），大写自动加 SHIFT
- `CSI r;cR` → 终端报告事件（RTT）
- 坐标映射：cell 中心 `(local+0.5)*desktop/viewport` clamp 到 desktop-1；letterbox 外丢弃；连续 MouseMoveEvent 合并只留最新

### 6. 输入注入（input backend）

事件模型：`KeyEvent{action: 0=press/1=release/2=tap, modifiers: SHIFT=1/ALT=2/CTRL=4, key_code, unicode}`；KeyCode：CHARACTER=0, ENTER=1, ESCAPE=2, BACKSPACE=3, TAB=4, UP/DOWN/RIGHT/LEFT=5-8, HOME=9, END=10, PAGE_UP=11, PAGE_DOWN=12, INSERT=13, DELETE=14, F1-F12=20-31。接口：`key/move/button(1-3,pressed)/scroll(amount clamp ±20)/close`（close 释放所有按住的键/按钮）。

| 后端 | 机制 |
|---|---|
| X11 XTest | keysym 名表→keycode；字符键扫描 min..max_keycode×4 level（level 1/3 补 Shift）；修饰 Control_L/Alt_L/Shift_L；顺序：修饰按→主键按→主键放→修饰放；滚轮=按钮4/5重复 ≤20 次 |
| ydotool | 每次事件 spawn `ydotool` CLI（2s 超时；构造时 `ydotool debug` 验证 daemon）；`key --key-delay 0 <mod>:1 <code>:1 <code>:0 <mod>:0`；无修饰字符走 `type --key-delay 0 -- <char>`；`mousemove --absolute`；`click --next-delay 0 <hex>`（按下 0x40/释放 0x80，base 左0右1中2）；滚轮 `mousemove --wheel 0 ±20`；evdev 键码表（ESC=1,ENTER=28,F1-10=59-68,HOME=102,UP=103 等；修饰 CTRL=29,SHIFT=42,ALT=56） |
| Mutter | D-Bus `org.gnome.Mutter.RemoteDesktop.Session`：`NotifyKeyboardKeysym(ub)`（≤0xFF 用 Latin-1，否则 `0x01000000\|codepoint`）、`NotifyKeyboardKeycode(ub)`（修饰 evdev 码）、`NotifyPointerMotionAbsolute(sdd)`、`NotifyPointerButton(ib)`（左0x110/中0x112/右0x111）、`NotifyPointerAxisDiscrete(ui)`（axis=0，±20） |
| macOS Quartz | `AXIsProcessTrusted` 前置检查（回退 ApplicationServices ctypes）；`CGEventCreateKeyboardEvent`+`SetUnicodeString`+flag mask；`CGEventPost(kCGHIDEventTap)`；macOS 虚拟键码表（ENTER=36,TAB=48,BS=51,ESC=53,方向123-126,F1=122）；拖拽按已按按钮切换事件类型；`CGEventCreateScrollWheelEvent` |
| Windows SendInput | ctypes user32.SendInput；虚拟桌面坐标归一化 0-65535（`MOUSEEVENTF_ABSOLUTE\|VIRTUALDESK`）；字符走 `KEYEVENTF_UNICODE`（UTF-16LE 代理对）；VK 表（BS=0x08,ENTER=0x0D,ESC=0x1B,F1-12=0x70+）；滚轮 data=amount×120 |

### 7. 会话调度（精确参数，必须照搬）

- **LatestFramePump**：采集线程按 active_fps 定速抓帧，只保留最新帧；覆盖未消费帧 `dropped_frames+=1`；generation 计数热更新目标尺寸/帧率；resize 作废在途帧；`latest_after(seq, timeout=0.02)` 条件等待
- **活跃 FPS**：Kitty 60 / ANSI 30；`--max-fps`/`SSHDESK_MAX_FPS` 1-120
- **idle 降频**（只降呈现，采集不降）：活动 <1s 全速；1-5s min(fps,30)；≥5s min(fps,2)
- **背压限帧**：latency ≥500ms→10fps；≥250→15；≥100→30；再叠加 `min(rate, 1000/(write_ms*2))`；下限 1fps
- **auto render scale**：latency≥250ms 或 write≥30ms → ×0.75（下限0.5）；≥100/≥14 → ×0.85；latency<60 且 0<write<8 → ×1.08（上限1.0）；降档冷却 2s、升档冷却 8s
- **帧复用**：content_digest 相同则复用上一 RenderedFrame
- **主循环**：主线程渲染+同步 write_all（memoryview 循环写，短写/阻塞重试，永不丢字节）；输入独立线程；SIGHUP/SIGTERM handler 只置停止事件；resize 轮询 → 清 previous + reset canvas + 立即重绘
- **RTT**：每秒 `CSI 6n`；未应答探测的已过时间也计入估计延迟

### 8. Agent 通道

**NDJSON session 协议**（`sshdesk-agent session`）：stdin 逐行 JSON（≤65536 字节），响应单行紧凑 JSON + `\n` + flush；EOF 退出 0。
- 成功 `{"id":<回显>,"ok":true,...}`；失败 `{"id":<id或null>,"ok":false,"error":"..."}`；超行 `{"ok":false,"error":"request is too large"}`（无 id）
- actions：`info`（→platform/session/capture/input/width/height）、`observe|screenshot`（max_width clamp 0-4096 → width/height/format:"png"/image_base64，PNG compress_level=1 双线性缩放）、`move{x,y}`、`click{x,y,button,count clamp 1-20,间隔0.05s}`、`scroll{amount clamp ±20,x,y}`、`type{text ≤16384,interval_ms clamp 0-1000}`、`key{key∈KEY_NAMES,ctrl,alt,shift}`、`wait{seconds clamp 0-10}`、`quit`（→quit:true 后退出）
- 坐标 clamp -16384..65535；错误不致命（会话继续）

**allowlist**（agent-ssh）：恰 1 参数 ≤65536 字节；shlex.split 后 basename 必须恰为 `sshdesk-agent`；禁 `--output`；否则 126/2。

**sshdesk-remote**：target 校验 `^[A-Za-z0-9_.%+@:-]{1,255}$`；`--timeout`（默认30，>0）；子命令同 agent；响应上限 64MiB；session 子命令直接 exec 透传。

**sshdesk-split**：tmux `split-window`（-h/-v，left/up 加 -b，-p size 20-80 默认 50）；开启 `allow-passthrough on`；无 tmux 则新建 detached session。

### 9. 统计仪表

计数：bytes_sent/received、full/delta/captured/dropped 帧；滑动窗口（60 样本）：capture/render/diff/encode/write/frame_age ms、changed%；fps=最近 1s 帧数；tx/rx 字节率每 0.25s 刷新。Ctrl+S 叠加 4 行（第 2 行起，白字/底 (22,28,38)）：FPS+变化率 / 各阶段毫秒 / 带宽+RTT / 尺寸+帧计数。

### 10. Bench

`SyntheticCapture(1920,1080,animate)` + ANSI 渲染器，30fps 目标，统计非 UNCHANGED 帧编码字节，按秒分桶求峰值；另测 100 次 parser 解析延迟。输出：Session duration / Average FPS / Average & Peak bandwidth (Kbit/s) / Input parse latency / Full & Delta frames / Average changed area。

### 11. 配置与部署面

- `/etc/sshdesk/<user>.conf`（root:root 0644）：key=value 白名单解析（16 个固定 key，绝不 eval），默认值 `DISPLAY=:0`、`XAUTHORITY=$HOME/.Xauthority`、`RUN_AS=<account>`、SSHDESK_*=auto；**文件覆盖进程环境**；`RUN_AS` 校验 `^[A-Za-z0-9_.-]+$`
- sshd 片段 `/etc/ssh/sshd_config.d/90-sshdesk-<user>.conf`：`Match User` + `ForceCommand` + `PermitTTY yes` + 禁全部转发 + `Match all`；含 Include 补齐、备份、`sshd -t` 验证回滚
- sudoers `/etc/sudoers.d/sshdesk-<account>`（仅 RUN_AS 分离时，0440，visudo 校验）：env_keep 白名单 + 无参 server + 受限 agent-ssh，绝不给 root
- ydotoold：`/usr/local/libexec/sshdesk/ydotoold`（pinned 1.0.4 + SHA-256 校验）、systemd 强沙箱 unit、`/etc/modules-load.d/sshdesk-uinput.conf`
- 安装脚本：install.sh（Linux/macOS 引导，apt/dnf/yum/pacman/zypper/apk 六包管理器、Wayland 家族判定、Tailscale 最后可选）、install.ps1（Windows：OpenSSH 能力包、标记块编辑 sshd_config、防火墙规则、自我提权）、install-server.sh、install-macos.sh、install-windows.ps1、configure-sshd.sh

### 12. 测试与 CI

9 个测试文件覆盖：agent 协议/allowlist、NDJSON 会话、capture 纯逻辑、终端解析器/坐标映射、安装脚本静态断言、真实 PTY 生命周期集成测试、调度策略参数、渲染/diff/量化/探测。CI：ubuntu（py3.10/3.13）+ macos + windows 三平台 unittest + ruff + 打包 + PowerShell 语法校验。

---

## 第二部分：Go 架构设计

### 单二进制多命令

一个 `sshdesk` 二进制，按 `argv[0]` basename 或第一个子命令分发（busybox 风格）。安装时创建 9 个同名符号链接，完全兼容原 forced-command/sudoers/sshd 配置中的路径。

### 包布局

```
cmd/sshdesk/            main：argv[0]/子命令分发
internal/
  config/               /etc/sshdesk/<user>.conf 白名单解析、默认值、文件覆盖环境
  platform/             平台/会话检测（X11/Wayland/GNOME/macOS/Windows）
  capture/              Frame{RGB24, digest, desktop 坐标} + ScreenCapture 接口
    x11/  xshm/  ffmpeg/  wayland/  gnome/  native_*/  synthetic/
  input/                KeyEvent/KeyCode/Modifiers + InputBackend 接口
    terminal/           终端转义序列解析器（状态机）
    x11/  ydotool/  mutter/  quartz/  sendinput/
  render/               Renderer 接口、Viewport、Cell、FrameUpdate
    ansi/               半块渲染 + 颜色量化 + writer
    kitty/              tile 渲染 + octree 量化 + PNG/base64 分块 + writer
    probe/              图形探测
  session/              DirectSession 主循环、LatestFramePump、TerminalState、stats
  agent/                agent 命令集、NDJSON session、allowlist
  client/               remote、split
  forcedcmd/            forced-command 路由、shell 选择器
  bench/                基准
scripts/                安装脚本（改写为安装 Go 二进制，逻辑与原脚本对齐）
```

### 关键依赖选型

| 功能 | 选型 |
|---|---|
| blake2s | `golang.org/x/crypto/blake2s` |
| termios/PTY | `golang.org/x/term`、`golang.org/x/sys/unix`；测试用 `github.com/creack/pty` |
| D-Bus（GNOME/Mutter） | `github.com/godbus/dbus/v5` |
| GStreamer（GNOME 管线） | cgo `github.com/go-gst/go-gst`（**最大技术风险**，见风险节） |
| X11 MIT-SHM/XTest | cgo libX11/libXext/libXtst（与原项目 ctypes 调用一一对应） |
| X11 XCB 兜底 | `github.com/jezek/xgb` GetImage（纯 Go，替代 Pillow ImageGrab） |
| macOS Quartz | cgo CoreGraphics/ApplicationServices |
| Windows | `golang.org/x/sys/windows`（user32 SendInput、BitBlt 截图） |
| PNG | 标准库 `image/png`（Encoder 设 CompressionLevel） |
| 128 色 octree 量化 | **自研**（Go 标准库无等价物；需实现 FASTOCTREE 等价量化器，允许字节级差异，视觉与体积等价即可） |
| 图像缩放 | `golang.org/x/image/draw`（Bilinear/CatmullRom 对应 BICUBIC/LANCZOS 语义） |
| 外部子进程 | ffmpeg、grim、spectacle、gnome-screenshot、ydotool、tmux、ssh 均 `os/exec` 固定参数向量（不过 shell） |

### 平台构建约束

- 构建标签拆分：`capture/native_darwin.go`（cgo Quartz）、`capture/native_windows.go`、`input/quartz_darwin.go`、`input/sendinput_windows.go` 等
- Linux 无 cgo 时降级链：ffmpeg → xgb GetImage（跳过 xshm）；有 cgo 时完整三级
- CI 矩阵：ubuntu / macos / windows，`go test ./...` + `go vet` + `gofmt`

---

## 第三部分：分阶段开发计划

### 阶段 0：脚手架（0.5 天）
- go.mod、包骨架、CI（三平台 test/vet/fmt）、LICENSE/README 沿用
- 验收：`go build ./...` 通过，CI 绿

### 阶段 1：核心会话闭环（synthetic 可跑通）
- config 白名单解析 + 单测
- 事件模型（KeyCode/Modifiers/KeyEvent）+ InputBackend/Capture/Renderer 接口
- 终端能力推断（capabilities）+ ANSI 渲染器 + writer（量化、diff、enter/leave、顶栏、标题栈）
- 终端输入解析器完整状态机（含 SGR/X10 鼠标、Ctrl+]/Ctrl+S、35ms ESC 消歧、8192 上限）
- synthetic capture + LatestFramePump + DirectSession 主循环（idle/背压/auto-scale 全套参数）+ TerminalState（raw/alt screen/恢复）+ stats + Ctrl+S overlay
- 验收：`sshdesk-server --capture synthetic --no-input` 在真实终端可用；移植 test_render/test_input/test_performance/test_lifecycle（creack/pty 集成测试）全绿

### 阶段 2：agent 通道 + 客户端
- agent 命令集 + NDJSON session + allowlist + 退出码语义
- sshdesk-remote、sshdesk-split、forced-command 路由（含 shell 选择器）
- bench
- 验收：移植 test_agent/test_agent_session 全绿；`sshdesk-remote localhost session` 手工验证

### 阶段 3：Linux X11
- ffmpeg 流式采集（stderr 环形缓冲）+ xshm（cgo）+ xgb 兜底 + 三级降级链 + 几何轮询
- XTest 输入（keysym 扫描/修饰顺序/释放清理）
- 验收：X11 桌面实测；`--check` 通过；移植 test_capture X11 相关

### 阶段 4：Linux Wayland
- grim/spectacle/gnome-screenshot 截图采集（选择顺序、5s 超时）
- ydotool CLI 输入（键码表、构造时 daemon 探测）
- 验收：wlroots/KDE 桌面实测

### 阶段 5：GNOME Wayland（高风险）
- godbus/dbus：DisplayConfig 多显示器并集、RemoteDesktop/ScreenCast 会话、PipeWireStreamAdded
- go-gst cgo 管线 + appsink 拉帧 + Mutter 联动输入
- 验收：GNOME Wayland 实测持久流（非幻灯片）；GNOME 布局计算单测
- **降级预案**：若 go-gst 不可行，保留 wayland 截图路径并明确文档说明差异

### 阶段 6：Kitty 像素渲染器
- 图形探测（含 tmux passthrough、TIOCGWINSZ）
- tile 布局/diff/15% 阈值/双缓冲全幅替换/octree 量化/PNG 分块/并行编码
- 像素鼠标 `?1016`、同步输出 `?2026`、程序生成光标
- 验收：kitty/ghostty/wezterm + tmux 实测；移植 test_render kitty 部分、test_lifecycle kitty 探测集成测试

### 阶段 7：macOS / Windows
- macOS：Quartz 采集 + 输入（cgo，权限检查）；Windows：BitBlt 采集 + SendInput
- 验收：两平台 `--check` + 手动会话

### 阶段 8：安装脚本与打磨
- 改写全部安装脚本安装 Go 单二进制 + 符号链接（sshd 片段/sudoers/ydotoold unit/配置语义不变）
- install.sh / install.ps1 引导
- 全量文档（docs/ 同步改写）、CHANGELOG
- 验收：Linux 干净机器一键安装 → ssh 连接 → 桌面/shell/agent 三路径全部可用

---

## 第四部分：风险与决策

1. **GStreamer cgo 绑定**（阶段 5）是最大不确定项。go-gst 需要 gstreamer 开发头文件；备选方案：a) 直接 cgo 调 gst C API 的迷你绑定（只需 pipeline + appsink 约 20 个函数）；b) 子进程 `gst-launch-1.0 ... ! fdsink` 读 raw 帧（无依赖绑定，行为等价，推荐作为首选——比 go-gst 更简单且与原 ffmpeg 后端同构）。
2. **FASTOCTREE 量化**无现成 Go 库，需自研八叉树量化（128 色无抖动）。字节级输出不必与 Pillow 一致，视觉/体积等价即可。
3. **xshm/XTest 需要 cgo**。若要求纯 Go 静态二进制，可用 xgb 的 SHM 扩展与 XTEST 扩展协议实现（xgb 带 xtest/shm 子包），优先评估纯 Go 路线，cgo 作备选。
4. **配置优先级语义**（文件覆盖环境）与 shell 版 forced-command 完全一致，Go 版需单测钉死。
5. **测试移植**：Python 测试中的 shell 脚本字符串断言类（test_installer）需重新设计为对 Go 生成配置内容的单测；其余基本可逐一对应移植。

---

## 第五部分：完成状态（2026-09-13）

- [x] **阶段 0：脚手架** — go.mod、包骨架、CI 三平台 test/vet/fmt
- [x] **阶段 1：核心会话闭环** — config 白名单、事件模型、capabilities、ANSI
  渲染器、终端输入状态机、synthetic capture、LatestFramePump、DirectSession、
  TerminalState、stats；render/input/performance/lifecycle 测试移植全绿
- [x] **阶段 2：agent 通道 + 客户端** — agent 命令集、NDJSON session、
  allowlist、remote、split、forced-command 路由、bench
- [x] **阶段 3：Linux X11** — ffmpeg 流采集、xshm、xgb 兜底、三级降级链、
  XTest 输入（纯逻辑单测 + 交叉编译；真机验证见遗留清单）
- [x] **阶段 4：Linux Wayland** — grim/spectacle/gnome-screenshot 截图采集、
  ydotool CLI 输入（假命令冒烟；真机验证见遗留清单）
- [x] **阶段 5：GNOME Wayland** — godbus Mutter 会话 + gst-launch-1.0 fdsink
  持久流（采用风险节备选 b，未用 go-gst）+ mutter 联动输入
- [x] **阶段 6：Kitty 像素渲染器** — 图形探测、tile 布局/diff/双缓冲、
  自研八叉树量化、并行编码、?1016/?2026、tmux passthrough；PTY 集成测试全绿
- [x] **阶段 7：macOS / Windows** — Quartz cgo 采集/输入（macOS 开发机
  实测通过）、BitBlt/SendInput（仅交叉编译 + 纯逻辑单测）
- [x] **阶段 8：安装脚本与打磨** — scripts/ 全部改写为单二进制 + 符号链接
  分发；README/docs×5/CHANGELOG 同步；internal/installer 静态断言；
  CI 加 scripts job 并对齐 Go 1.27

### 遗留验证清单（需真机/真环境，开发机无法覆盖）

1. Linux X11 真会话：三级降级链、XTest 输入、`--check` 全链路。
2. wlroots/KDE Wayland 真桌面：grim/spectacle 采集 + ydotoold 输入。
3. GNOME Wayland 真会话：Mutter/PipeWire 持久流（非幻灯片）+ RemoteDesktop
   输入联动。
4. Kitty/Ghostty/WezTerm 真终端：图形探测、tile 渲染、tmux passthrough。
5. Windows 真机：BitBlt 采集、SendInput、终端生命周期、install.ps1 全程。
6. Linux 干净机器一键安装验收：install.sh → OpenSSH 配置 → ssh 连接 →
   desktop/shell/agent 三路径（阶段 8 原始验收项）。
7. GitHub releases 发布流水线：脚本约定的 `sshdesk-<os>-<arch>` +
   `.sha256` 产物尚未有对应 release/CI 发布流程。
