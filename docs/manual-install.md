# SSHDESK（Go 版）手动安装指南

本文档介绍**不使用一键脚本**，手动构建、安装并启用 SSHDESK 的完整步骤。
目标环境以 Linux 宿主机为主（推荐），附 macOS 手动安装说明。

> 一键安装（`scripts/install.sh` / `scripts/install.ps1`）会自动完成本文的
> 全部步骤。只有在需要审查每一步、或一键脚本不支持你的环境时才需要手动安装。

## 0. 工作原理（30 秒版）

SSHDESK 不是服务，不监听任何端口。它是一个由 OpenSSH `ForceCommand`
拉起的普通程序：

```
ssh 客户端 ──► sshd ──ForceCommand──► /usr/local/bin/sshdesk-forced-command
                                          ├── 精确 desktop → 桌面会话（需 PTY，唯一提权路径）
                                          ├── 无参数       → 认证账户登录 shell
                                          └── 其他任意命令 → 登录 shell -c <原文>（与标准 SSH 逐字一致）
```

因此安装 = 放二进制 + 建符号链接 + 写配置文件 + 配 sshd 的 Match 块。
认证、加密、传输全部由 OpenSSH 负责。

## 1. 准备依赖

### 1.1 构建依赖

- Go 1.23+（构建机用；也可以交叉编译后只拷贝二进制，宿主机无需 Go）
- Linux 宿主机运行时依赖：
  - `openssh-server`
  - 按显示服务器选择采集/输入工具（见下表）
- 可选：`tmux`（`sshdesk-split` 需要）、`ffmpeg`（X11 首选采集路径）

| Linux 会话 | 采集 | 输入 | 需要安装 |
|---|---|---|---|
| X11（任意桌面） | FFmpeg/XCB → MIT-SHM → XCB | XTest | `ffmpeg`（可选但推荐）；libX11/libXext 运行时 |
| GNOME Wayland | Mutter + PipeWire/GStreamer 持久流 | Mutter RemoteDesktop | `gstreamer1.0-tools`、`gstreamer1.0-pipewire`、`gstreamer1.0-plugins-base`、PipeWire |
| KDE Plasma Wayland | `spectacle` | `ydotool` + `ydotoold` | `spectacle`、ydotool ≥1.0.4 |
| wlroots（Sway/Hyprland 等） | `grim` | `ydotool` + `ydotoold` | `grim`、ydotool ≥1.0.4 |

> GNOME 走 Mutter 合成器 API，无需 ydotool，也无需任何特权助手。
> KDE/wlroots 的输入需要 `ydotoold` 能访问 `/dev/uinput`（见第 5 节）。

### 1.2 构建二进制

```bash
git clone https://github.com/rarnu/sshdesk-go.git
cd sshdesk-go

# 本机构建
go build -o sshdesk ./cmd/sshdesk

# 或交叉编译 Linux 版本（如在 macOS 上构建）
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o sshdesk ./cmd/sshdesk
```

## 2. 安装二进制与符号链接

所有命令都是同一个二进制，按 `argv[0]` 分发：

```bash
sudo install -m 0755 sshdesk /usr/local/bin/sshdesk
cd /usr/local/bin
sudo ln -sf sshdesk sshdesk-server
sudo ln -sf sshdesk sshdesk-local
sudo ln -sf sshdesk sshdesk-bench
sudo ln -sf sshdesk sshdesk-forced-command
sudo ln -sf sshdesk sshdesk-agent
sudo ln -sf sshdesk sshdesk-agent-ssh
sudo ln -sf sshdesk sshdesk-remote
sudo ln -sf sshdesk sshdesk-split
```

> `scripts/install-server.sh` 会替你做这一步以及第 3、6 节：
> `sudo ./scripts/install-server.sh "$USER" "$DISPLAY" "${XAUTHORITY:-$HOME/.Xauthority}"`

## 3. 写配置文件 `/etc/sshdesk/<用户>.conf`

按 SSH 账户各一份，root 拥有、0644：

```bash
sudo mkdir -p /etc/sshdesk
sudo tee /etc/sshdesk/alice.conf <<'EOF'
DISPLAY=:0
XAUTHORITY=/home/alice/.Xauthority
RUN_AS=alice
SSHDESK_RENDER=auto
SSHDESK_COLOR=auto
SSHDESK_MOUSE=auto
SSHDESK_UNICODE=auto
SSHDESK_X11_CAPTURE=auto
SSHDESK_MAX_FPS=auto
SSHDESK_SCALE=auto
EOF
sudo chown root:root /etc/sshdesk/alice.conf
sudo chmod 0644 /etc/sshdesk/alice.conf
```

要点：

- 解析是**固定 16 键白名单**（不 eval）：`DISPLAY`、`XAUTHORITY`、`RUN_AS`、
  `WAYLAND_DISPLAY`、`XDG_RUNTIME_DIR`、`XDG_SESSION_TYPE`、`XDG_CURRENT_DESKTOP`、
  `DBUS_SESSION_BUS_ADDRESS`、`YDOTOOL_SOCKET`、`SSHDESK_RENDER`、`SSHDESK_COLOR`、
  `SSHDESK_MOUSE`、`SSHDESK_UNICODE`、`SSHDESK_X11_CAPTURE`、`SSHDESK_MAX_FPS`、
  `SSHDESK_SCALE`
- **文件中的值会覆盖进程环境变量**
- `RUN_AS` 是桌面画面所属的用户；默认等于 SSH 账户本人（见第 6 节了解分离场景）
- Wayland 会话需按会话类型补充（在已登录的图形会话里 `env | grep -E 'WAYLAND|XDG|DBUS'` 抄过来）：

```text
WAYLAND_DISPLAY=wayland-0
XDG_RUNTIME_DIR=/run/user/1000
XDG_SESSION_TYPE=wayland
XDG_CURRENT_DESKTOP=GNOME
DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus
```

## 4. 验证后端可用性

以桌面用户身份、在图形会话环境中运行：

```bash
/usr/local/bin/sshdesk-server --check
# 期望输出类似：
# SSHDESK check passed: 1920x1080 X11Capture capture; input=enabled
```

- X11：确保 `DISPLAY`/`XAUTHORITY` 指向活跃 X 会话
- GNOME Wayland：确保 `DBUS_SESSION_BUS_ADDRESS` 等变量正确，且
  `gst-inspect-1.0 pipewiresrc` 存在
- 失败时按报错提示补齐权限/工具后再继续，不要跳过本步

## 5.（仅 KDE/wlroots）安装并运行 ydotoold

```bash
# 1) 安装 ydotool ≥ 1.0.4 与 ydotoold（发行版包或官方 release）
# 2) 加载 uinput 模块并持久化
echo uinput | sudo tee /etc/modules-load.d/sshdesk-uinput.conf
sudo modprobe uinput

# 3) 以桌面用户身份运行 ydotoold（可做成 systemd 用户/系统服务；
#    scripts/install.sh 生成的系统级沙箱 unit 可直接参考）
sudo -u alice YDOTOOL_SOCKET=/run/sshdesk-ydotool/socket ydotoold &

# 4) 在 /etc/sshdesk/alice.conf 中记录 socket 路径
#    YDOTOOL_SOCKET=/run/sshdesk-ydotool/socket
```

ydotoold 必须能访问 `/dev/uinput`，但**绝不要**用 root 运行 sshdesk 本体。

## 6.（可选）专用 SSH 账户与 sudoers

想让桌面画面属于另一个图形用户（例如 SSH 登录账户 `sshdesk`、桌面属于
`alice`），可用独立登录账户，只有桌面路径以桌面用户身份执行：

```bash
sudo useradd --create-home --shell /bin/bash sshdesk
sudo ./scripts/install-server.sh sshdesk :0 /home/alice/.Xauthority alice
```

此时 `/etc/sshdesk/sshdesk.conf` 里 `RUN_AS=alice`，并生成
`/etc/sudoers.d/sshdesk-sshdesk`（0440，visudo 校验）：

```text
Defaults:sshdesk env_keep += "DISPLAY XAUTHORITY WAYLAND_DISPLAY XDG_RUNTIME_DIR XDG_SESSION_TYPE XDG_CURRENT_DESKTOP DBUS_SESSION_BUS_ADDRESS YDOTOOL_SOCKET SSHDESK_RENDER SSHDESK_COLOR SSHDESK_MOUSE SSHDESK_UNICODE SSHDESK_X11_CAPTURE SSHDESK_MAX_FPS SSHDESK_SCALE TERM"
sshdesk ALL=(alice) NOPASSWD: /usr/local/bin/sshdesk server ""
```

要点：只允许以**桌面用户**身份（不是 root）执行无参 server——这是
调度器唯一会提权的路径；登录 shell 与任意远程命令永远以认证账户本人
运行、绝不提权，与标准 SSH 完全一致。

## 7. 配置 sshd（生效的关键一步）

生成 Match 块并写入 sshd 配置目录：

```bash
./scripts/configure-sshd.sh alice |
  sudo tee /etc/ssh/sshd_config.d/90-sshdesk-alice.conf
```

内容如下（`configure-sshd.sh` 输出，含尾部 `Match all`）：

```sshconfig
Match User alice
    ForceCommand /usr/local/bin/sshdesk-forced-command
    PermitTTY yes
    DisableForwarding yes
    X11Forwarding no
    AllowTcpForwarding no
    AllowAgentForwarding no
    PermitTunnel no
    GatewayPorts no
    PermitUserRC no
Match all
```

然后验证并重载：

```bash
# 若主配置没有 Include /etc/ssh/sshd_config.d/*.conf，先补上
sudo cp /etc/ssh/sshd_config /etc/ssh/sshd_config.before-sshdesk
sudo sshd -t && sudo systemctl reload ssh    # 部分发行版服务名叫 sshd
# sshd -t 失败则恢复备份：sudo mv /etc/ssh/sshd_config.before-sshdesk /etc/ssh/sshd_config
```

> **警告**：配置 forced command 期间请保留另一个可用的管理登录（控制台或
> 另一个 SSH 账户）。任何能认证到该账户的人都能看到并控制活动图形会话，
> 等同于物理控制台访问。

## 8. 连接使用

只有精确输入 `desktop` 才进入桌面；除此之外一切行为与标准 SSH 一致：

```bash
# 普通登录 shell（标准 SSH 行为，以认证账户身份）
ssh alice@server

# 桌面（唯一选择器，必须有 PTY）
ssh -t alice@server desktop

# 任意其他远程命令 → 登录 shell -c 逐字执行（与无 ForceCommand 时一致，
# 引号/管道/重定向/退出码全由本账户 shell 解释）
ssh alice@server systemctl --user status

# agent 命令（无 PTY，经标准 shell 通道执行 PATH 内的 sshdesk-agent）
ssh alice@server sshdesk-agent info
ssh alice@server sshdesk-agent screenshot --max-width 1280 > desktop.png
```

会话内控制：

- 直接键入 = 发送键盘输入；终端鼠标 = 移动/点击/拖拽/滚轮
- `Ctrl+S` 切换统计叠加
- `Ctrl+] Ctrl+]` 退出（本地处理，不会注入远端）
- 调整终端窗口大小会触发新视口与全量重绘

可选：客户端本地构建同一份代码即可使用辅助命令：

```bash
sshdesk-remote alice@server screenshot --output desktop.png
sshdesk-remote alice@server session        # NDJSON 长会话
sshdesk-split alice@server                 # tmux 左右分屏
```

## 9. macOS 手动安装（开发/手动会话）

```bash
go build -o sshdesk ./cmd/sshdesk
./scripts/install-macos.sh        # 装到 ~/.local/bin（9 个符号链接）
```

- 需在 系统设置 → 隐私与安全性 中给运行 sshdesk 的进程授予
  **屏幕录制**（采集）与**辅助功能**（输入）权限
- 验证：`sshdesk-server --check`
- macOS 上配置 OpenSSH forced-command 托管属于高级用法，参考
  `scripts/install.sh` 的 macOS 段（sshd 片段 +
  `systemsetup -setremotelogin on`）

## 10. 故障排查

| 症状 | 排查 |
|---|---|
| `--check` 报 no Linux graphical session | `DISPLAY`/`WAYLAND_DISPLAY` 未进配置；确认第 3 节变量 |
| GNOME 报 gst-launch/pipewire 错误 | 缺 `gstreamer1.0-pipewire` 或 `gstreamer1.0-tools` |
| 输入无效（KDE/wlroots） | `ydotool debug` 是否成功；`YDOTOOL_SOCKET` 与 uinput 权限 |
| 连接后立刻退出 | 必须分配 PTY（不要用 `ssh -T`）；看 `sshd -t` 与 journal |
| 画面模糊/低分辨率 | 终端不支持 Kitty graphics 属预期（ANSI 回退）；换 kitty/ghostty/wezterm 验证 |
| 延迟高 | 会话内 `Ctrl+S` 看 RTT/带宽；设 `SSHDESK_SCALE=0.75` 降像素 |
| shell 路径权限异常 | 属预期：shell 永远以认证账户本人运行，不继承桌面用户身份 |

## 11. 卸载

使用仓库自带脚本（Linux 需 sudo/root；macOS 为用户级卸载）：

```bash
# 交互确认后卸载（先列出将删除的清单）
sudo ./scripts/uninstall.sh --user alice

# 跳过确认；保留 /etc/sshdesk/alice.conf
sudo ./scripts/uninstall.sh --user alice --yes
sudo ./scripts/uninstall.sh --user alice --keep-config
```

脚本按安全顺序执行：先删 sshd 片段 → `sshd -t` 验证 → reload OpenSSH →
删 sudoers 规则 → 删 `/etc/sshdesk` 配置（`--keep-config` 时保留）→
删二进制与 9 个符号链接（只删确认指向 sshdesk 的符号链接与二进制本体，
他人同名文件一律保留）→ 停用并删除 sshdesk-ydotoold 服务与 pinned
ydotool（仅 Linux 且确由我们安装时）。**不会**触碰 OpenSSH 本体、
sshd_config 主配置里的 Include 行（通用配置）、Tailscale 与其他系统包。

手动等价步骤（参考，可简化）：

```bash
sudo rm -f /etc/ssh/sshd_config.d/90-sshdesk-alice.conf
sudo sshd -t && sudo systemctl reload ssh    # 部分发行版服务名叫 sshd
sudo rm -f /etc/sudoers.d/sshdesk-alice /etc/sshdesk/alice.conf
sudo systemctl disable --now sshdesk-ydotoold.service 2>/dev/null
sudo rm -f /etc/systemd/system/sshdesk-ydotoold.service \
  /etc/modules-load.d/sshdesk-uinput.conf \
  /usr/local/libexec/sshdesk/ydotoold /usr/local/bin/ydotool
sudo systemctl daemon-reload
cd /usr/local/bin && sudo rm -f sshdesk sshdesk-server sshdesk-local \
  sshdesk-bench sshdesk-forced-command sshdesk-agent sshdesk-agent-ssh \
  sshdesk-remote sshdesk-split
```
