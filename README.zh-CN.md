# SSHDESK

```text
       _____ _____ __  ______  ____________ __ __
      / ___// ___// / / / __ \/ ____/ ___// //_/
      \__ \ \__ \/ /_/ / / / / __/  \__ \/ ,<
     ___/ /___/ / __  / /_/ / /___ ___/ / /| |
    /____//____/_/ /_/_____/_____//____/_/ |_|

        YOUR DESKTOP  //  ONE SSH SESSION  //  ZERO EXTRA PORTS
```

[![Tests](https://github.com/rarnu/sshdesk-go/actions/workflows/test.yml/badge.svg)](https://github.com/rarnu/sshdesk-go/actions/workflows/test.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**[English](README.md)**

> SSHDESK 是一个完整的交互式远程桌面：它完全跑在一条 SSH 会话里，
> 直接显示在你的终端中。

AI 编码代理在修改本仓库前必须先阅读 [AGENTS.md](AGENTS.md)。

## 演示

[![在 YouTube 上播放 SSHDESK 演示](https://img.youtube.com/vi/k9qGXJVsxW0/maxresdefault.jpg)](https://www.youtube.com/watch?v=k9qGXJVsxW0 "在 YouTube 上播放 SSHDESK 演示")

（演示视频录制自 Python 原版实现；Go 重写版以单个静态二进制提供完全
相同的会话体验。）

SSHDESK 以 OpenSSH forced command 的方式运行：认证、加密、传输全部由
OpenSSH 负责，SSHDESK 不实现 SSH、不监听任何端口。没有浏览器、没有
定制客户端、没有 VNC/RDP 监听、没有第二套口令库、也没有 web 服务。
Kitty、Ghostty、WezTerm 会收到锐利的真实像素 tile；普通 ANSI 终端
则使用半块（half-block）彩色单元渲染器，因此 OpenSSH、PuTTY、手机
客户端和嵌入式终端全都可用。

整个应用就是一个静态 Go 二进制。九个命令名——`sshdesk`、
`sshdesk-server`、`sshdesk-local`、`sshdesk-bench`、
`sshdesk-forced-command`、`sshdesk-agent`、`sshdesk-agent-ssh`、
`sshdesk-remote`、`sshdesk-split`——都是这个二进制的 busybox 式
符号链接或子命令。

## 连接方式

用你已有的 SSH 客户端即可。只有精确输入 `desktop` 这个远程命令才会
进入图形会话，其余一切都是标准 SSH 行为：

```bash
# 标准登录 shell（OpenSSH 行为不变）
ssh user@server

# SSHDESK 桌面（显式选择器；必须分配 PTY，所以用 -t）
ssh -t user@server desktop

# 任意其他远程命令都逐字交给登录 shell 的 -c 执行
ssh user@server sshdesk-agent info
```

路由语义刻意保持朴素：不带命令的连接打开认证账户的登录 shell；除精确
的 `desktop` 选择器之外的任何命令，都传给该 shell 的 `-c`，表现与
没有安装 forced command 时完全一致。键盘、鼠标、终端 resize、变化的
像素和会话清理都走同一条 SSH PTY。按 `Ctrl+] Ctrl+]` 退出桌面。

> [!WARNING]
> 任何能认证到 SSHDESK 账户的人都可以运行 `desktop` 选择器，看到并
> 控制活动的图形会话——请把它当作物理控制台访问来对待。配置 forced
> command 期间，务必保留第二个可用的管理登录。

## 安装

不再有 curl 管道一键脚本。从
[release](https://github.com/rarnu/sshdesk-go/releases) 下载 `sshdesk`
二进制，或从源码检出自行构建（`go build -o sshdesk ./cmd/sshdesk`，
需要 Go 1.27+），然后用二进制内置的跨平台安装器完成安装。安装器不
下载任何内容、不安装系统包；它会按当前会话体检采集/输入依赖，缺失时
只打印包名建议。

### Linux

以 root 运行安装器（OpenSSH server 需已安装）：

```bash
sudo ./sshdesk --install            # 自动检测的用户不对时加 --user alice
```

它会安装二进制与 `/usr/local/bin` 下的 8 个命令符号链接，写入按账户的
`/etc/sshdesk/<user>.conf`，向 `/etc/ssh/sshd_config.d` 添加 forced
command 片段（经 `sshd -t` 校验，失败即回滚），reload OpenSSH，在
非 GNOME 的 Wayland 会话上配置沙箱化的 `ydotoold` 输入助手，并验证
桌面访问。Linux 专有参数：`--display`、`--xauthority`、`--run-as`。

在 Wayland 上，运行安装器时保留已登录图形会话的环境变量，以便写入
配置：

```bash
sudo --preserve-env=WAYLAND_DISPLAY,XDG_RUNTIME_DIR,XDG_SESSION_TYPE,\
XDG_CURRENT_DESKTOP,DBUS_SESSION_BUS_ADDRESS,YDOTOOL_SOCKET \
  ./sshdesk --install
```

要让桌面属于另一个图形用户，可用专用 SSH 账户，仅桌面路径提权：

```bash
sudo useradd --create-home --shell /bin/bash sshdesk
sudo ./sshdesk --install \
  --user sshdesk --display :0 \
  --xauthority /home/alice/.Xauthority --run-as alice
```

生成的 sudoers 规则只允许以图形用户身份执行无参数的桌面 server，绝不
授予 root；登录 shell 与远程命令永远以认证账户本人运行。

### macOS

安装是用户级的；用 sudo 再跑一次会额外写入 sshd 片段并启用
Remote Login：

```bash
./sshdesk --install
sudo ./sshdesk --install            # 可选：sshd 片段 + Remote Login
```

macOS 仍需要在 系统设置 → 隐私与安全性 中为安装后的二进制授予
屏幕录制（Screen Recording）与辅助功能（Accessibility）权限。

### Windows

在管理员权限的 PowerShell 中运行安装器：

```powershell
.\sshdesk.exe --install
```

管理员安装会把 `sshdesk.exe` 放到 `%ProgramData%\SSHDESK`，把二进制
目录写入系统 PATH，向 `sshd_config` 添加 forced command 标记块
（先校验、失败即回滚），创建 OpenSSH 防火墙规则并启动服务。没有
管理员权限时降级为 `%LOCALAPPDATA%` 下的用户级安装。Windows 的
OpenSSH 通常运行在 Session 0，因此 forced command 桌面采集属于实验性
功能，必须能够到达已登录的交互桌面。

### 采集与输入依赖

`sshdesk --install` 会检查下列工具并按检测到的包管理器给出安装建议，
但不会代为安装：

| Linux 会话 | 采集 | 输入 |
|---|---|---|
| X11（任意桌面） | FFmpeg/XCB → MIT-SHM → XCB（推荐 `ffmpeg`） | XTest |
| GNOME Wayland | Mutter + PipeWire/GStreamer 持久流 | Mutter RemoteDesktop API |
| KDE Plasma Wayland | `spectacle` | `ydotool` ≥ 1.0.4 + `ydotoold` |
| wlroots（Sway、Hyprland 等） | `grim` | `ydotool` ≥ 1.0.4 + `ydotoold` |

GNOME 需要 GStreamer 命令行工具（`gst-launch-1.0`）、base 插件以及
GStreamer 的 PipeWire 插件。非 GNOME 的 Wayland 输入要求 `ydotoold`
能访问 `/dev/uinput`；不要用 root 运行 SSHDESK server 本体。

### Tailscale（可选）

需要跨局域网访问时，请自行安装 Tailscale（Linux 上例如
`curl -fsSL https://tailscale.com/install.sh | sh`）。Tailscale 只是
在私有 tailnet 上承载普通 OpenSSH 流量，不替代 OpenSSH，也不增加第二
种认证方式。

## 卸载

```bash
sudo sshdesk --uninstall            # Linux；加 --yes 跳过确认
sshdesk --uninstall                 # macOS 用户级；sudo 会连带删除 sshd 片段
```

卸载器只删除安装器创建的内容——sshd 片段、sudoers 规则、
`/etc/sshdesk` 配置、二进制及其符号链接或包装、ydotoold 助手——完成后
校验并 reload OpenSSH，同名外来文件一律保留。OpenSSH 本体、
`sshd_config` 的 Include 行以及所有系统包都不会被触碰。`--keep-config`
可保留 `/etc/sshdesk/<user>.conf`。每一步的手动等价操作见
[手动安装指南](docs/manual-install.md)。

## 配置

安装器会向 `/etc/sshdesk/<user>.conf` 写入安全默认值：

```text
DISPLAY=:0
XAUTHORITY=/home/alice/.Xauthority
RUN_AS=alice
SSHDESK_RENDER=auto
SSHDESK_COLOR=auto
SSHDESK_MOUSE=auto
SSHDESK_UNICODE=auto
SSHDESK_X11_CAPTURE=auto
SSHDESK_MAX_FPS=auto
SSHDESK_SCALE=1.0
```

解析采用固定的 16 键白名单（`DISPLAY`、`XAUTHORITY`、`RUN_AS`、6 个
Wayland 会话键、7 个 `SSHDESK_*` 调优键），不做任何 shell 求值；
文件中的值会覆盖进程环境变量。`RUN_AS` 是桌面画面所属的账户，默认
等于 SSH 登录账户本人。Wayland 会话下安装器会写入从活跃图形会话采集
到的变量；X11 会话只写 `DISPLAY` 与 `XAUTHORITY`。完全检测不到图形
会话（headless）时仍写入上述 X11 默认值，但会打印醒目警告并跳过桌面
访问检查——待该用户登录图形会话后重跑 `sudo sshdesk --install` 即可。

## 控制与调优

- 正常键入即发送键盘输入（Ctrl/Alt/Shift、方向键、F1–F12）
- 终端鼠标用于移动、点击、拖拽与滚轮滚动
- `Ctrl+S` 切换实时统计浮层
- `Ctrl+] Ctrl+]` 永远在本地退出，不会注入远端
- 调整终端窗口大小会触发新视口与全量重绘，不断开连接

性能目标：活跃时 sharp 60 FPS / ANSI 30 FPS，空闲自适应降频，背压下
采用最新帧调度（丢弃过期工作而不是累积延迟），客户端跟不上时自动
降低 scale。`SSHDESK_RENDER=kitty` 需要 Kitty graphics；`ansi` 强制
通用回退。`SSHDESK_X11_CAPTURE=auto` 依次尝试持续排干的 FFmpeg/XCB
流、MIT-SHM、XCB。`SSHDESK_MAX_FPS` 接受 1–120。`SSHDESK_SCALE`
接受 0.25–1.0 的固定值（慢链路可用 0.75 减少像素）或 `auto` 动态
调整；安装器默认写入 `1.0`（完整细节）。

## Agent 与自动化

Agent 的 computer-use 命令就是普通远程命令：`ssh user@server
sshdesk-agent info` 经标准 shell `-c` 通道运行 `sshdesk-agent`，而该
命令集只解析固定语法，绝不把收到的 shell 字符串拿去求值。更严格的
`sshdesk-agent-ssh` allowlist 包装仍然保留，供受限部署把自己的 forced
command 指向它；默认调度器不经过它。任何能通过 SSH 执行 CLI 命令的
agent 都可以接入，不依赖特定框架或模型。按已知坐标编写的脚本动作
不需要视觉；要动态探索陌生桌面，则需要视觉能力或独立的 OCR/图像分析
工具——因为观察结果是截图，而不是无障碍语义树。

```bash
ssh user@server sshdesk-agent info
ssh user@server sshdesk-agent screenshot --max-width 1280 > desktop.png
ssh user@server sshdesk-agent move 900 500
ssh user@server sshdesk-agent click 900 500 --button left
ssh user@server sshdesk-agent scroll -3 900 500
ssh user@server sshdesk-agent type hello
ssh user@server sshdesk-agent key enter
```

在本地安装 SSHDESK 后可用 `sshdesk-remote`，获得可靠引用与有界的
NDJSON（逐行 JSON）响应：

```bash
sshdesk-remote user@server info
sshdesk-remote user@server screenshot --output desktop.png
sshdesk-remote user@server click 900 500
sshdesk-remote user@server type 'text with spaces'
```

长时间运行的 agent 可以保持一条 NDJSON session：

```bash
sshdesk-remote user@server session
{"id":1,"action":"observe","max_width":1280}
{"id":2,"action":"click","x":900,"y":500,"button":"left"}
{"id":3,"action":"type","text":"hello"}
{"id":4,"action":"quit"}
```

想把本地 agent shell 放在远程可视化桌面旁边，安装 `tmux` 后运行
`sshdesk-split user@server`：右窗格用显式 `desktop` 选择器打开桌面，
左窗格留给 `sshdesk-remote`。标准 OpenSSH `ControlMaster` 配置可以把
这些会话复用在同一条连接上；SSHDESK 不会另外打开任何服务或端口。

## 平台支持

Linux 是主要的、完整集成的宿主机平台；任意操作系统都可以作为 SSH
客户端，因为可视化协议就是 SSH 上的标准终端输出。

| 宿主 | 采集 | 输入 | 说明 |
|---|---|---|---|
| Linux X11 | FFmpeg/XCB → MIT-SHM → XCB 三级降级链 | XTest | 推荐宿主 |
| Linux GNOME Wayland | Mutter + PipeWire 持久流 | Mutter RemoteDesktop | 无特权助手 |
| Linux KDE / wlroots | `spectacle` / `grim` 截图 | `ydotool` + `ydotoold` | 沙箱化 uinput 服务 |
| macOS | Quartz（感知 Retina） | Quartz CGEvent | 需屏幕录制 + 辅助功能权限 |
| Windows | BitBlt 虚拟桌面 | SendInput | 仅限交互会话；forced command 为实验性 |

后端精确行为见[平台支持](docs/platforms.md)，终端支持情况见
[客户端兼容性](docs/compatibility.md)。

## 安全

任何能认证的人都能看到并控制活动的图形会话——请把 SSHDESK 账户当作
物理控制台访问对待，配置 forced command 期间保留第二个管理登录。
生成的 sudoers 规则只允许以桌面属主身份执行无参数的桌面 server，
绝不授予 root；登录 shell 与远程命令永远以认证账户本人运行。生成的
`Match` block 在所有路径上禁用转发、隧道与 agent forwarding。Wayland
输入助手以沙箱化 systemd 服务运行，设备策略仅限 `/dev/uinput`。完整
安全模型见[安全与权限](docs/security.md)。

## 开发与测试

Go module 目标版本为 Go 1.27，构建产物是单个静态二进制：

```bash
go build -o sshdesk ./cmd/sshdesk
go test ./...
go vet ./...
gofmt -l .

# 交叉编译检查
GOOS=linux GOARCH=amd64 go build ./...
GOOS=linux GOARCH=arm64 go build ./...
GOOS=windows GOARCH=amd64 go build ./...

# 基准测试：精确测量渲染后的终端字节
./sshdesk bench --duration 60 --columns 100 --rows 30 --color 256
```

采集、渲染、输入、会话管理与终端输出分属独立包，各后端可以独立演进，
而 OpenSSH 用户体验保持不变。

## 文档

- [架构与数据流](docs/architecture.md)
- [平台支持](docs/platforms.md)
- [客户端与终端兼容性](docs/compatibility.md)
- [安全与权限](docs/security.md)
- [基准测试方法](docs/benchmark.md)
- [手动安装指南](docs/manual-install.md)
- [更新日志](CHANGELOG.md)
- [开发规划与状态](PLAN.md)

## License

[MIT](LICENSE)

## 致谢

SSHDESK 起源于 Python 项目
[rylena/sshdesk](https://github.com/rylena/sshdesk)；本仓库是它的 Go
重写版，上方演示视频录制的正是最初的 Python 实现。锐利渲染器的思路
受 [Desktui](https://github.com/mishushakov/desktui) 启发：终端图像
像素与变化 tile 能够保留远超字符画的桌面细节。
