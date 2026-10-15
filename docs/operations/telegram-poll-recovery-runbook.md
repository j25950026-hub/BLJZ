# Telegram 轮询恢复运行手册

## 目标

机器人是否可用，以最近一次成功完成 Telegram `getUpdates` 响应解析为准。进程存活、数据库可用、`/healthz` 返回 200 都不足以证明机器人能接收命令。

本机制分为三层：机器人写入功能状态，独立 Guardian 执行有限恢复，现有 `/admin` 后台提供手机查看和受控人工请求。后台不会直接执行 `systemctl`、Shell、文件路径或任意版本切换。

## 运行状态

| 信号 | 含义 | 自动行为 |
| --- | --- | --- |
| `starting` | 服务在 90 秒启动宽限内 | 等待首个成功轮询 |
| `healthy` | 最近成功轮询未过期 | 不执行恢复 |
| `stale` | 最近成功已超过 90 秒，且连续失败至少 3 次 | 按错误类型进入有限恢复 |
| `missing` / `invalid` | 状态文件缺失或无效 | 视为不可用，最多一次受控重启 |
| `configuration`（401） | Token 或配置错误 | 冻结，不重启、不回滚 |
| `ownership_conflict`（409） | 另一个实例正在轮询 | 冻结，不重启、不回滚 |
| `rate_limited`（429） | Telegram 限流 | 冻结，不重启、不回滚 |
| `transient` / `unknown` | 网络或未知错误 | 一次重启，失败后冻结 |
| `release_regression` | 例如重复 `context canceled` | 一次重启，失败后可试一次已验证版本回滚 |

Guardian 的冷却期为 15 分钟。一次事件最多执行一次重启和一次试回滚；任何终态失败都会进入 `frozen`，不会循环重启或反复切换版本。

## 已验证版本

自动回滚只解析独立的 `rollback-candidate`，绝不解析或猜测 `previous`。`last-known-good` 表示当前已验证版本；当前版本首次通过两次连续成功轮询晋升时，晋升前的旧 `last-known-good` 会原子保存为 `rollback-candidate`。

一个 release 必须同时满足以下条件才能成为 `last-known-good`：

- 位于 `releases/<release-id>/`，且 `current`、`last-known-good`、`rollback-candidate` 链接未逃出该目录。
- `manifest.json` 的二进制 SHA-256 与实际 `ledgerbot` 文件一致。
- 当前 Poll Runtime 报告的 release ID 与 SHA-256 与 manifest 一致。
- 同一 release 已连续完成至少两次成功 Long Polling；任意失败会把连续成功计数清零。

自动回滚候选还必须与 `current` 指向不同 release。没有符合这些条件的候选版本时，Guardian 会冻结并保留现场，而不会把“最近旧版本”误当作可回滚版本。

## 手机后台

日常入口是现有 `/admin`：登录后总控台顶部显示 Telegram 输入状态、最近成功轮询、连续失败类别、当前 release 和 Guardian 阶段。该区域每 30 秒局部刷新，不影响其他后台页面。

当输入状态异常时：

1. 使用当前主设备会话打开总控台。
2. 点击 `受控恢复`，输入当前 Google Authenticator 验证码并确认。
3. 后台仅写入一个带操作者和请求 ID 的固定 `recover` 请求，5 分钟后自动失效。
4. Guardian 在下一次定时执行中消费该请求，执行一次固定服务重启并等待功能性健康恢复。

只有轮询错误被归类为 `release_regression` 时，才会显示 `试回滚至已验证版本`。该操作同样要求主会话、严格同源请求、CSRF 和新鲜 TOTP；它只能尝试独立的 `rollback-candidate`，不接受浏览器输入的版本号、路径或命令。

若后台本身不可用，Guardian 仍会在主机上独立运行。整台主机、网络或磁盘出现故障时，使用云厂商手机控制台作为最后的人工入口；不要把 root SSH 私钥常驻在手机上作为日常操作方式。

## 发布准备

以下本地脚本只建立不可变 release 布局，不重启服务：

```bash
bash scripts/deploy_release_layout.sh \
  --root /opt/bilibili-ledgerbot-20260704 \
  --release-id <release-id> \
  --ledgerbot <ledgerbot-binary> \
  --guardian <ledgerbot-guardian-binary> \
  --expected-ledgerbot-sha <sha256>
```

它会写入 `releases/<release-id>/`、`manifest.json`、`release.env`，再原子切换 `current`。新 release 初始 `rollback_eligible=false`；只有两次连续成功 Long Poll 通过 Guardian 检查后，才会被提升为 `last-known-good`。

`scripts/ledgerbot-guardian.service`、`scripts/ledgerbot-guardian.timer` 和 `scripts/bilibili-ledgerbot.service.v2` 是待安装模板。本轮没有安装到 `/etc/systemd/system`、没有 daemon-reload、没有 enable/start timer、没有切换生产 `current` 链接。

## 本地验证

```bash
go test ./internal/system ./internal/bot ./internal/admin ./internal/config ./cmd/ledgerbot ./cmd/ledgerbot-guardian -count=1
bash scripts/testing/test_release_layout.sh
bash scripts/testing/test_guardian_units.sh
node --check web/admin-app/app.js
```

生产前必须单独确认的操作包括：创建或修改生产 release 目录/链接、安装或修改 systemd 单元、daemon-reload、启用 Guardian timer、重启机器人、以及任何真实回滚演练。
