# SSHDESK Go 重写 — 开发约定

这是 SSHDESK（原 Python 项目 `/Users/rarnu/Code/github/sshdesk`）的 Go 重写版。
行为规格以原 Python 实现为准，逐参数移植；阶段规划见 `PLAN.md`。

## 构建与验证

```sh
go build ./...      # 构建
go vet ./...        # 静态检查
gofmt -l .          # 必须无输出
go test ./...       # 全部测试
```

提交前四项必须全绿。internal/session 含时序敏感的 PTY 测试，改动后建议
`go test -count=3 ./internal/session/` 防抖动漏检。

三平台交叉编译必须全部通过（session 的终端代码分 unix/windows 两套文件）：

```sh
GOOS=darwin GOARCH=arm64 go build ./...   # macOS 本机默认 cgo 开启
GOOS=linux GOARCH=amd64 go build ./...
GOOS=linux GOARCH=arm64 go build ./...
GOOS=windows GOARCH=amd64 go build ./...
```

手工冒烟（二进制名必须是 `sshdesk`，argv[0] 分发只认
`sshdesk` / `sshdesk-server` / `sshdesk-local` 等 9 个命令名）：

```sh
go build -o sshdesk ./cmd/sshdesk
./sshdesk server --capture synthetic --no-input --check
# 期望: SSHDESK check passed: 1280x720 SyntheticCapture capture; input=disabled
```

注意：macOS 上 input=auto 已接 Quartz，无 Accessibility 权限时去掉
`--no-input` 的 --check 会按 Python 文案报权限错误并退出 1（预期行为）。

安装脚本改动后须跑静态断言与语法检查：

```sh
go test ./internal/installer/
for script in scripts/*.sh; do sh -n "$script"; done
```

## 退出码

- 0：正常 detach / 正常退出 / NDJSON 会话 EOF 或 quit
- 1：运行错误（后端不可用、非交互终端、渲染配置错误、SSH 失败、超时等）
- 2：命令行参数/协议违规（argparse 类错误、allowlist 的 --output 禁令、
  target/size/timeout 校验失败）
- 126：agent-ssh allowlist 拒绝（basename 非 sshdesk-agent）
- 130：被信号中断（SIGINT/SIGTERM，与 128+SIGINT 一致）

## 包布局

- `internal/config`：SSH 环境白名单（16 个 SSHDESK_* 键）解析，文件覆盖环境
- `internal/capture`：Frame / ScreenCapture 接口、`capture.Name`（Python 风格
  类名）；`capture/synthetic` 为合成桌面；`capture/ffmpeg` 为 FFmpeg/XCB
  子进程流采集（固定 argv 向量、stderr 排干保留尾部 2048 字节、懒启动、
  TERM→KILL 停止序列）；`capture/xshm` 为 MIT-SHM 层（xgb shm 扩展 +
  x/sys SysV shm 包装，32bpp BGRX 校验，CatmullRom 缩放对应 Pillow
  LANCZOS）；`capture/x11` 为组合后端（ffmpeg→xshm→xgb GetImage 降级链、
  每秒几何轮询、SSHDESK_X11_CAPTURE 环境选择，Linux-only）；
  `capture/wayland` 为截图助手采集（grim stdout PNG、gnome-screenshot/
  spectacle 临时文件，5s 超时，助手选择级联与 Python 逐字一致；
  exec-based 故全平台可编译可测）；`capture/gnome` 为 GNOME 持久流采集
  （godbus D-Bus：DisplayConfig 布局并集为纯逻辑、RemoteDesktop/ScreenCast
  会话、PipeWireStreamAdded 订阅；gst-launch-1.0 fdsink 子进程流替代
  appsink，进程管理与 ffmpeg 后端同构；CreateInputBackend 联动 mutter）；
  `capture/native` 为 macOS/Windows 原生采集（darwin：cgo Quartz
  CGDisplayCreateImage + 逻辑像素 Retina 缩放 + CGEventGetLocation 光标；
  windows：user32 GetDC + gdi32 BitBlt 虚拟桌面，syscall LazyDLL 无 cgo；
  共享 BGRA→RGB/target size/digest 逻辑可移植）
- `internal/input`：事件模型与注入后端接口；`input/terminal` 为终端输入解析
  状态机；`input/x11` 为 XTEST 注入后端（keysym 表/扫描/事件序列为纯逻辑，
  X 接线 Linux-only）；`input/ydotool` 为 ydotool CLI 输入（evdev 键码表、
  构造时 `ydotool debug` 探测 daemon、2s 超时、exec-based 全平台可测）；
  `input/mutter` 为 GNOME RemoteDesktop D-Bus 输入（keysym/BTN 表、
  坐标 clamp、调用序列纯逻辑，Caller seam 注入 godbus）；
  `input/quartz` 为 macOS CGEvent 输入（darwin cgo：AXIsProcessTrusted
  前置检查、虚拟键码表/修饰 mask/拖拽事件类型切换/滚轮 ±20 clamp，
  poster seam 注入，逻辑层全平台可测）；`input/sendinput` 为 Windows
  SendInput 输入（syscall LazyDLL 无 cgo：0..65535 虚拟桌面归一化 +
  ABSOLUTE|VIRTUALDESK、KEYEVENTF_UNICODE 代理对、VK 表、滚轮 ±20×120，
  sender seam 注入，逻辑层全平台可测）
- `internal/render`：Capabilities / 更新模型；`render/ansi` 为 ANSI 渲染器与 writer；
  `render/probe` 为 Kitty 图形探测（纯解析可移植；POSIX 执行：raw 模式
  切换 + TIOCGWINSZ 像素尺寸 + poll 读超时，Windows 恒不可用）；
  `render/kitty` 为 Kitty 像素渲染器（tile 布局/diff/双缓冲全幅替换、
  自研 128 色八叉树量化对应 Pillow FASTOCTREE、tile ≥4 时 goroutine
  并行编码保序拼接、tmux DCS passthrough）
- `internal/session`：DirectSession 渲染/输入主循环、LatestFramePump、
  TerminalState（raw 模式 + 备用屏幕进出与恢复）、统计；renderPipeline
  抽象 ANSI/Kitty 双渲染管线（pipeline.go），SSHDESK_RENDER 探测选择，
  ?1016 像素鼠标经 kitty.TranslatePixelCoordinates 路由
- `internal/agent`：agent 命令集（info/screenshot/move/click/scroll/type/key/
  session）、NDJSON 会话协议、agent-ssh allowlist（126/2 退出码）、自实现
  POSIX shlex；capture/input 经工厂函数惰性创建（Linux 按 detect_platform
  接 X11/Wayland/GNOME，darwin 接 native+quartz，windows 接
  native+sendinput，SetBackends/SetPlatform 为测试注入接缝）
- `internal/forcedcmd`：ForceCommand 路由（desktop/shell/agent），Deps 结构
  注入全部副作用便于 dry-run 测试；RUN_AS 分离时经 /usr/bin/sudo -n 固定
  参数向量提权（不过 shell）；login shell 用 argv[0] 加 - 前缀（对应
  Python 的 -l），Windows 走 COMSPEC 且 exec 降级为子进程
- `internal/client`：sshdesk-remote（target 白名单正则、固定 ssh argv、
  64MiB 响应上限、超时错误）与 sshdesk-split（tmux split-window 向量、
  allow-passthrough、不在 tmux 内时新建 detached session）
- `internal/bench`：SyntheticCapture(1920x1080) + ANSI 30fps 带宽基准，
  输出格式与 Python 逐字对齐
- `internal/installer`：仅含测试的静态断言包（对应原 test_installer.py）：
  sh -n 语法、sudoers 不含 root、env_keep 16 键、sshd Match 块、ydotool
  pinned SHA-256、Wayland 依赖顺序、Tailscale 最后、符号链接齐全、
  Windows 安装器锚点；pwsh/powershell 存在时做 PowerShell 解析检查
- `scripts/`：安装脚本（语义逐行对齐原 Python 仓库脚本）：install.sh
  （Linux/macOS 引导，六包管理器，下载 releases 二进制 + SHA-256 校验，
  GNOME 探测改为 gst-launch-1.0/gst-inspect-1.0 pipewiresrc）、
  install-server.sh（装 /usr/local/bin/sshdesk + 8 个符号链接，sudoers
  指向 `sshdesk server`/`sshdesk agent-ssh` 子命令）、configure-sshd.sh、
  install-macos.sh（~/.local 用户级 9 链接）、install.ps1 +
  install-windows.ps1（下载 exe + Get-FileHash 校验，9 个 .cmd 子命令
  包装）。二进制来源优先级：SSHDESK_BINARY → SSHDESK_SOURCE_DIR 本地
  构建（有 go 则自动 go build）→ GitHub releases 下载
- `cmd/sshdesk`：单一二进制，argv[0] busybox 式分发（9 个命令名）+
  server/local/forced-command/agent/agent-ssh/remote/split/bench 子命令；
  capture/input 后端选择在 backends*.go（Linux 按检测接 X11/Wayland/
  GNOME，darwin auto 接 native+quartz，windows auto 接 native+sendinput）

## PTY 测试注意事项（macOS 实测）

- 子进程用 `SysProcAttr{Setsid: true}`（对应 Python 测试的
  start_new_session=True）。**不要加 Setctty**：拿到控制终端的子进程退出时
  会被 macOS 内核卡在 exit 状态（ttywait），kill -9 都清不掉。
- pty master 不支持 `os.File.SetReadDeadline`（静默返回 ErrNoDeadline）。
  测试读取用 `syscall.SetNonblock` + EAGAIN 轮询，见
  `internal/session/lifecycle_test.go` 的 ptyReader。
- 等待 `cmd.Wait()` 结果时，轮询条件不能直接读 channel（会把值消费掉，
  导致后续断言永远超时）；用 atomic 标志做条件、channel 传结果。
- 子进程以 pty slave 为 stdout 时，若 master 侧无人排干，写满内核缓冲后
  子进程会阻塞在 write；测试等待退出期间必须持续读取 master。

## 当前阶段的已知边界

- Linux X11 后端（capture/x11 组合链、capture/ffmpeg、capture/xshm、
  input/x11）已实现但**未在真实 X 会话上验证**（开发机无 X 环境，
  仅有纯逻辑单元测试与交叉编译保障）。
- Wayland 截图采集与 ydotool 输入同样**未在真实 Wayland 会话上验证**；
  已用假 grim/gnome-screenshot/ydotool 命令在 PATH 上做过端到端冒烟
  （选择级联、临时文件、daemon 探测失败路径），真实 wlroots/KDE 桌面
  实测仍缺。
- GNOME 后端（capture/gnome、input/mutter）**未在真实 GNOME 会话上验证**；
  D-Bus 流程序列、布局解析、Stop 顺序、mutter 调用序列均有 fake bus /
  recorder 单测，真实 GNOME Wayland 持久流实测仍缺（阶段验收项）。
  行为差异：Python 用 appsink 按需 try-pull-sample（2s 超时），Go 用
  gst-launch-1.0 fdsink 持续流按 w*h*3 整帧读取（与 ffmpeg 后端同构），
  失败后同样重建流重试一次；mutter/ydotool 事件失败同样被静默丢弃
  （接口无错误返回）。依赖检查从 PyGObject/pipewiresrc 插件变为
  gst-launch-1.0 可执行文件探测。godbus 为纯 Go 库，gnome/mutter 包与
  阶段 4 一样全平台可编译（无 linux 构建标签）。
- ydotool 事件失败被静默丢弃：input.Backend 接口无错误返回，Python 版
  会在事件注入失败时抛 RuntimeError；构造时的 daemon 探测错误仍如实上抛
  （--check 可验证）。命令超时按 "ydotool input timed out after 2 seconds"
  报错（Python 直接传播 TimeoutExpired 异常）。
- xshm 与 xcb 兜底仅支持 32bpp BGRX（掩码 0xFF0000/0x00FF00/0x0000FF）
  根视觉，其它布局报 "unsupported X11 pixel layout for accelerated
  capture"（Python 的 Pillow 兜底无此限制）。
- 缩放用 x/image/draw 的 CatmullRom 对应 Pillow 的 LANCZOS/BICUBIC
  （任务点名的近似，像素级输出与 Python 不一致）。
- 降级链不区分错误类型：Python 只对 OSError 降级，Go 对 tiers 的任何
  error 都按可降级处理（ffmpeg/xshm 显式指定时仍直接抛出）。
- macOS 原生后端（capture/native + input/quartz，cgo）**已在开发机实测**：
  `--capture native --no-input --check` 通过（2056x1329 逻辑分辨率，
  Retina 2x 缩放正确，本上下文有 Screen Recording 权限）；
  `--input quartz --check` 在无 Accessibility 权限时按 Python 文案报
  "grant Accessibility permission to the SSH/Python process for input
  control" 并退出 1（AXIsProcessTrusted 路径正确；未改动系统权限）。
  注意：新版 macOS SDK 把 CGDisplayCreateImage 标记为 15.0 obsoleted，
  cgo 需 `-mmacosx-version-min=14.0`（仅 CFLAGS，LDFLAGS 加会触发链接
  版本告警）；SDK 27 中 CF 类型被 cgo 映射为 uintptr（判空用 ==0）。
- Windows 原生后端（capture/native BitBlt + input/sendinput）**未经真机
  验证**：仅 GOOS=windows CGO_ENABLED=0 交叉编译与纯逻辑单测（VK 表、
  归一化、事件序列、滚轮 data）保障；INPUT 结构体按 x64 40 字节布局
  手工对齐。`--capture auto` 在 macOS/Windows 现已接真实原生后端，
  无权限时按上述文案失败（与 Python 一致），不再是 synthetic 兜底。
- agent 的 capture/input 工厂在 darwin/windows 已接真实原生后端
  （quartz 无 Accessibility 权限时 agent 命令同样报权限错误）。
- Kitty 像素渲染已实现并经 PTY 集成测试验证（模拟终端应答走完
  探测→a=T 放置→?1016h→退出清理全程），但**未在真实 Kitty/Ghostty/
  WezTerm 终端上实测**。已知偏差：
  - PNG 量化为自研八叉树（≤128 色，叶均值取整），与 Pillow
    FASTOCTREE 的调色板与 PNG 字节不一致（视觉/体积等价，任务已允许）；
    Go png 编码器还会按调色板大小选 1/2/4/8 bit 位深。
  - 图形探测仅 POSIX（Windows 恒不可用，SSHDESK_RENDER=kitty 在
    Windows 报 "terminal does not support Kitty graphics..."）。
  - 上游 Python 有两个 kitty 测试与自身实现矛盾而失败
    （test_renderer_accepts_prescaled_frame_with_desktop_coordinates
    期望未取整的像素视口 800x450，实际实现产出 800x440；
    test_kitty_renderer_never_upscales_the_remote_desktop 断言放置
    视口不放大，与实现注释「终端按 cell span 放大放置」直接冲突）。
    Go 测试断言真实实现行为（内容图像不超过桌面原生分辨率、
    像素视口按整 cell 取整）。
- Windows 输入读超时不生效，ESC 消歧等下一字节（见 PLAN.md 阶段说明）。
- shlex 在 Windows 上也用 POSIX 规则（Python 在 Windows 用非 POSIX 模式
  保留引号；forced-command/agent-ssh 实际部署在 POSIX 服务端）。
- forcedcmd 的 login shell 用 argv[0] 加 - 前缀实现（Python 用 -l 参数，
  效果等价）；/etc/passwd 查不到目录服务账户时回退 $SHELL→/bin/sh。
- screenshot 双线性缩放为自实现（无新依赖），PNG 用 BestSpeed 档对应
  Python 的 compress_level=1。
- 安装脚本（阶段 8）语义逐行对齐原 Python 仓库脚本，仅有静态断言
  （internal/installer）与 sh -n 保障，**未在 Linux 干净机器上做过一键
  安装 → ssh 连接 → desktop/shell/agent 三路径的端到端实测**（阶段 8
  验收项仍缺）。差异点：
  - 产物为单二进制：install-server.sh 装 /usr/local/bin/sshdesk + 8 个
    符号链接；sudoers 规则为 `/usr/local/bin/sshdesk server ""` 与
    `/usr/local/bin/sshdesk agent-ssh *`（forcedcmd 提权时
    os.Executable 解析到真实二进制路径，子命令形式）；sshd ForceCommand
    仍指向 sshdesk-forced-command 符号链接（argv[0] 分发）。
  - 二进制来源优先级 SSHDESK_BINARY → SSHDESK_SOURCE_DIR 本地构建
    （校验 go.mod，有 go 则自动 go build ./cmd/sshdesk）→ GitHub
    releases 下载 `sshdesk-<os>-<arch>` + `<asset>.sha256` 校验；
    releases 产物命名是脚本约定，发布流水线尚未建立。
  - GNOME 依赖探测从 PyGObject/pipewiresrc 改为 gst-launch-1.0 +
    gst-inspect-1.0 pipewiresrc；包列表移除 python3-gi/gir1.2-*，
    apt 增加 gstreamer1.0-tools、apk 增加 gstreamer-tools。
  - 移除全部 python3/venv/pip 依赖与检测；删除 shell 版
    sshdesk-forced-command 包装脚本（白名单解析已在 forcedcmd 内）。
  - Windows 安装器下载 sshdesk-windows-amd64.exe + Get-FileHash 校验，
    9 个 .cmd 子命令包装；**未经 pwsh 实跑与 Windows 真机验证**
    （PowerShell 语法检查在本机 skip，CI Windows runner 会实跑）。
