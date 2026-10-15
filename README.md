# Telegram 查记机器人（Go 复刻版）

## 项目说明书

完整指令、权限、使用示例和部署维护说明见：

- [docs/项目指令说明书.md](docs/项目指令说明书.md)

## 快速启动

1. 复制环境文件：

```powershell
Copy-Item .env.example .env
```

2. 编辑 `.env`：

```text
BOT_TOKEN=你的机器人Token
DB_PATH=data/bot_go.db
BOT_TIMEZONE=Asia/Shanghai
LOG_LEVEL=INFO
```

风控相关可选配置（留空使用默认值）：

```text
REPLAY_WINDOW_SECONDS=60
LARGE_TXN_THRESHOLD=50000
```

说明：

- `REPLAY_WINDOW_SECONDS`：消息重放防护窗口，超过该秒数的记账/撤回消息将拒绝执行。
- `LARGE_TXN_THRESHOLD`：大额交易阈值（CNY），超过阈值会记录风险事件并在回执中提示。

3. 拉依赖并运行：

```powershell
go mod tidy
go run ./cmd/ledgerbot
```

## 核心实现模块

1. 命令路由与业务：`internal/bot/app.go`
2. 数据持久化：`internal/db/database.go`
3. 解析与算法：`internal/utils/utils.go`
4. 外部服务：`internal/services/*.go`

## 复刻与证据文档

1. 功能映射：`复刻表格.md`
2. 证据链路：`证据链路.md`

## 新增环境变量（联行号第三方接口）

可选配置，留空则使用内置总行联行号回退逻辑：

```text
BANK_BRANCH_API_URL=
BANK_BRANCH_API_TOKEN=
BANK_BRANCH_API_HEADER=Authorization
BANK_BRANCH_API_TIMEOUT_SEC=8
```

说明：

- `BANK_BRANCH_API_URL`：第三方联行号接口地址，程序会以 `GET` 方式携带 `card_no`、`bank_code`、`bank_name` 参数。
- `BANK_BRANCH_API_TOKEN`：可选鉴权令牌。
- `BANK_BRANCH_API_HEADER`：令牌头名，默认 `Authorization`。
- `BANK_BRANCH_API_TIMEOUT_SEC`：接口超时秒数，默认 8 秒。

## 新增环境变量（TG WebApp）

可选配置，用于私聊菜单中的 WebApp 群控入口：

```text
TG_WEBAPP_URL=https://你的域名/tg/webapp
TG_WEBAPP_SECRET=建议配置随机密钥
```

说明：

- `TG_WEBAPP_URL`：WebApp 页面地址。配置后，私聊 Inline 菜单会出现“打开WebApp”按钮。
- `TG_WEBAPP_SECRET`：WebApp 请求签名密钥；留空时回退使用 `BOT_TOKEN` 做签名。

## TG 界面能力

- 私聊 Inline 菜单：功能总览、群组管理、查单管理、入群教程、命令示例、更新日志、世界时间示例、群控入口。
- 私聊教程链路：`邀请进群/入群教程/图文教程/教程` 与 `/tutorial` 均可触发，支持方式A（一键邀请）/方式B（手动拉群）/初始化清单/常见问题，并联动群控与 WebApp 入口。
- 私聊订阅链路：`购买使用/续费`（或 `/buy`）可创建套餐订单，`我的订单`（或 `/orders`）可查看审核状态；后台可审批并自动续期。
- 群内 Inline 群控面板命令：`群控面板`。
- 群控面板快捷动作：记账开关、账单、查单、定时任务、自动回复列表、关键词转发列表、自定义内容列表、汇率、换算开关、自动回复开关、菜单帮助。
- WebApp 快捷执行：签名鉴权后走与群内一致的命令路由（权限受控）。

## 项目定位

这是一个基于 Go 的 Telegram 查记与群组运营机器人。程序入口是
`cmd/ledgerbot/main.go`，模块名为 `go-bot`，正常运行时由一个长期运行的
`ledgerbot` 进程接收 Telegram 更新并访问本地数据库和外部服务。

当前代码的主要职责边界：

- `internal/bot/`：Telegram 消息、命令、Inline 按钮、私聊菜单、群控面板和 WebApp 动作。
- `internal/db/`：SQLite 数据库、群账、查单、日切结算、提醒、缓存和审计相关持久化。
- `internal/accounting/`：金额解析、分录生成、汇率换算和精度处理。
- `internal/services/`：地址、汇率、银行卡、地理位置、对象存储等外部服务适配。
- `internal/admin/`：后台 HTTP 管理入口，生产环境由 `ADMIN_ADDR` 控制监听地址。
- `internal/system/`：结构化日志、健康探针、数据库连接池监控、备份与运行时监控。

后台使用单管理员登录。只有同时配置以下三项时，`/admin` 才会显示登录页：

```text
ADMIN_USERNAME=admin
ADMIN_PASSWORD_HASH='$2a$10$...'
ADMIN_TOTP_SECRET=BASE32_TOTP_SECRET
ADMIN_SETUP_TOKEN=<由运维安全渠道生成的高熵一次性口令>
```

`ADMIN_PASSWORD_HASH` 必须使用 bcrypt 哈希，并保留值两侧的单引号；bcrypt 哈希中的 `$` 不应被 `.env` 变量展开。登录需要管理员账号、密码和 Google Authenticator 六位验证码。

首次扫码是预置凭据后的绑定确认，不是网页写入 `.env` 的配置向导。运维先配置账号、bcrypt 哈希、TOTP 密钥和 `ADMIN_SETUP_TOKEN`，再访问 `/admin/setup`：输入一次性口令、账号和密码，服务端通过同源 API 返回短期 setup Cookie 和受保护的 PNG 二维码，扫码后输入验证码完成一次性消费。TOTP URI 不会出现在 URL、JSON、静态前端、日志或第三方二维码服务中；setup 完成后状态写入根数据库，服务重启不会重置。

后台登录允许同一单管理员账号在多台设备同时在线。首个有效 session 是主设备，服务端不自动过期；后续设备默认 12 小时且最长可由主设备延长到 30 天。`/admin/security` 需要当前设备重新输入 TOTP 后才能查看二维码或管理设备，支持查看设备、修改次设备过期时间、单台撤销、退出其他设备、注销当前设备和转移主设备。数据库只保存 Cookie bearer 的哈希，公开设备句柄与 Cookie 值相互独立。

私聊群控面板属于 `internal/bot/` 的业务能力。当前代码支持跨页选择全部可管理群，
也支持按已有群类型和已有群标签筛选后追加选择；条件菜单在群数量较多时分页显示。

## 线上部署事实

以下内容来自使用 `shuai` 目录中的 RSA 私钥登录服务器后的实际核查，不是按目录名推测：

- RSA 私钥位置：`/home/eren/桌面/shuai/deploy/ssh/orderflow_43_156_119_108_rsa`
- 服务器：`43.156.119.108`
- 主机名：`VM-0-8-ubuntu`
- 当前可用登录用户：`root`
- systemd 服务：`bilibili-ledgerbot.service`
- unit 文件：`/etc/systemd/system/bilibili-ledgerbot.service`
- 服务状态：已启用，核查时为 `active (running)`
- 运行用户：`root`
- 工作目录：`/opt/bilibili-ledgerbot-20260704`
- 环境文件：`/opt/bilibili-ledgerbot-20260704/.env`
- 启动文件：`/opt/bilibili-ledgerbot-20260704/ledgerbot`
- 重启策略：`Restart=on-failure`，失败后等待 5 秒重启

线上目录不是源码仓库，而是运行目录，已核实包含：

- 当前二进制：`/opt/bilibili-ledgerbot-20260704/ledgerbot`
- SQLite 数据库文件：`/opt/bilibili-ledgerbot-20260704/data/bot_go.db`
- 运行日志目录：`/opt/bilibili-ledgerbot-20260704/logs/`
- 多个带时间和变更说明的旧二进制备份

线上运行模式也已从 systemd 日志确认：

- Webhook 未启用。
- 当前使用 Telegram Long Polling。
- 健康探针监听 `*:18080`，接口为 `/healthz` 和 `/readyz`。
- 后台接口监听 `127.0.0.1:8090`；配置完整的单管理员登录后访问 `/admin` 显示登录页，未配置时 fail-closed 返回 `401`。
- 线上当前检查结果：`/healthz` 和 `/readyz` 均返回 `ok:true`。

生产环境中已核对到的非敏感配置包括：

```text
DB_PATH=data/bot_go.db
BOT_TIMEZONE=Asia/Shanghai
LOG_LEVEL=INFO
ADMIN_ADDR=127.0.0.1:8090
PRIVATE_TRIAL_DAYS=3
REPLAY_WINDOW_SECONDS=60
LARGE_TXN_THRESHOLD=50000
```

生产 `.env` 还包含机器人 token、后台 token、WebApp 密钥、银行卡接口密钥和
TronGrid API key 等敏感配置。README 不记录这些配置值，也不应把生产 `.env`
复制到 Git 仓库。

## 正式部署方向

当前线上采用“本地构建 Go 二进制，再由 root 通过 systemd 托管”的方式，不是
在服务器上执行 `go run`，也不是用 `nohup` 另起一个机器人进程。

### 1. 本地构建和测试

在本项目目录执行：

```bash
cd automation_system_export_20260218_172840/go-bot-work
go test ./internal/bot
go build -o "/tmp/ledgerbot-release" ./cmd/ledgerbot
sha256sum "/tmp/ledgerbot-release"
```

V2 发布包必须包含构建出的 `ledgerbot` 二进制和以下静态资源：

- `web/admin-app/index.html`
- `web/admin-app/app.js`
- `web/admin-app/styles.css`

`internal/admin` 会从运行目录读取这些文件；缺少它们时会回退到旧的内置页面。
不要把本地 `.env`、`data/`、测试数据库、运行日志或私钥上传到服务器。

### 2. 上传到线上暂存文件

使用 `shuai/deploy/ssh` 中的 RSA 直接登录 root。下面的命令只上传暂存文件，
不替换生产二进制，也不重启服务：

```bash
SSH_KEY="/home/eren/桌面/shuai/deploy/ssh/orderflow_43_156_119_108_rsa"
REMOTE="root@43.156.119.108"
REMOTE_DIR="/opt/bilibili-ledgerbot-20260704"

scp -i "$SSH_KEY" \
  "/tmp/ledgerbot-release" \
  "$REMOTE:$REMOTE_DIR/ledgerbot.next"

scp -i "$SSH_KEY" -r \
  "web/admin-app" \
  "$REMOTE:$REMOTE_DIR/web/"

ssh -i "$SSH_KEY" "$REMOTE" \
  "sha256sum $REMOTE_DIR/ledgerbot.next"
```

远程 SHA256 必须与本地 `sha256sum "/tmp/ledgerbot-release"` 的结果一致。
校验不一致时停止，不得继续替换。

### 3. 备份、替换和重启

以下步骤会修改生产文件并重启机器人，只能在确认版本和 SHA256 一致后执行。
V2 生产切换使用一次性 systemd timer，在确认的维护窗口执行；不要在工作时间直接
`systemctl restart`。切换脚本同时完成 `ledgerbot` 专用用户、探针绑定和 systemd
沙箱调整，并在启动失败时恢复旧二进制、unit 和 `.env`。

```bash
ssh -i "$SSH_KEY" "$REMOTE" '
  set -eu
  stamp=$(date +%Y%m%d_%H%M%S)
  cp -a /opt/bilibili-ledgerbot-20260704/ledgerbot \
    "/opt/bilibili-ledgerbot-20260704/ledgerbot.bak_${stamp}"
  mv /opt/bilibili-ledgerbot-20260704/ledgerbot.next \
    /opt/bilibili-ledgerbot-20260704/ledgerbot
  systemctl restart bilibili-ledgerbot.service
'
```

替换时只更新 `ledgerbot` 和 `web/admin-app/`，不要覆盖以下线上状态：

- `/opt/bilibili-ledgerbot-20260704/.env`
- `/opt/bilibili-ledgerbot-20260704/data/`
- `/opt/bilibili-ledgerbot-20260704/logs/`

### 4. 重启后的验证

```bash
ssh -i "$SSH_KEY" "$REMOTE" \
  "systemctl is-active bilibili-ledgerbot.service"

ssh -i "$SSH_KEY" "$REMOTE" \
  "curl -fsS http://127.0.0.1:18080/healthz"

ssh -i "$SSH_KEY" "$REMOTE" \
  "curl -fsS http://127.0.0.1:18080/readyz"

ssh -i "$SSH_KEY" "$REMOTE" \
  "journalctl -u bilibili-ledgerbot.service -n 100 --no-pager"
```

验收条件：

1. `systemctl is-active` 输出 `active`。
2. `/healthz` 和 `/readyz` 的 JSON 中包含 `"ok":true`。
3. 日志显示机器人重新启动，并且没有新的启动失败、端口占用或数据库初始化错误。
4. Telegram 侧只保留一个 Long Polling 实例，避免多个实例同时调用 `getUpdates`。

### 5. 回滚方向

回滚使用发布时生成的 `ledgerbot.bak_<时间戳>`，只恢复二进制，不恢复
`.env` 或数据库：

```bash
BACKUP_NAME="ledgerbot.bak_填写实际发布时间戳"

ssh -i "$SSH_KEY" "$REMOTE" "
  set -eu
  test -f \"$REMOTE_DIR/$BACKUP_NAME\"
  cp -a \"$REMOTE_DIR/$BACKUP_NAME\" \"$REMOTE_DIR/ledgerbot\"
  systemctl restart bilibili-ledgerbot.service
"
```

回滚后仍需重新执行 `systemctl is-active`、`/healthz`、`/readyz` 和
`journalctl` 检查。

## 运行维护边界

- 生产服务的唯一托管入口是 `bilibili-ledgerbot.service`。
- 不要在服务器上手动执行 `./ledgerbot` 或 `nohup ./ledgerbot`，否则可能与
  systemd 实例形成两个 Long Polling 消费者。
- 修改 `ADMIN_USERNAME`、`ADMIN_PASSWORD_HASH`、`ADMIN_TOTP_SECRET`、`ADMIN_SETUP_TOKEN`、`ADMIN_TOKEN`、`BOT_TOKEN`、WebApp 密钥或第三方 API key 时，只修改线上
  `.env`，并在变更后重启 systemd 服务；不要把值写进 README。
- 管理员账号、密码哈希或 TOTP 密钥的指纹变化会递增认证 epoch 并撤销所有旧设备 session；setup token 只受数据库 `consumed_at` 一次性状态门控。
- 修改数据库结构或执行批量修复前，先备份 `data/bot_go.db`，再使用项目提供的
  管理命令或迁移逻辑，不要直接删除生产数据库。
