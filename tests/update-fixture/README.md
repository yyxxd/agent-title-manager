# 应用内更新隔离验收

本目录提供可复现的**隔离**验收工具。它不触碰正式安装目录或正式数据，不发真实 GitHub 请求，不发布 Release。

## 组件

- `server.mjs`：本地 HTTP fixture，模拟 GitHub Releases 的最新稳定版接口。
- `prepare/main.go`：调用真实 `internal/update` 验签、下载，并启动真实 `atm-update.exe` helper。
- `seed/main.go`：用真实引擎 schema 在隔离数据目录初始化 SQLite 并写入标记用户数据。

## 隔离前置条件

1. 用临时测试密钥构建两个版本安装包：`build.ps1 -Version 0.2.0/0.2.1 -UpdatePublicKey <临时公钥>`。临时私钥仅存在于本地隔离文件，不提交。
2. 所有相关进程必须带 `ATM_DATA_DIR=<绝对路径>`，否则 `--check-uninstall` 会检查正式 scope。
3. 安装目录用 `setup.exe /S /NoIntegration /D=<绝对路径>`，必须是隔离目录。
4. 每轮开始前确保没有占用隔离安装目录 EXE 的进程（失败恢复会重启旧版应用）。
5. 隔离库须用 `seed/main.go` 生成；手工建简化 schema 会导致引擎启动时报 `table settings already exists`。

## 一次成功交接的观察

- `prepare` 输出 `handoff prepared for 0.2.1, starting helper`。
- `<ATM_DATA_DIR>/update-helper.log` 记录 `result ok=true message=已更新至 0.2.1`。
- `<ATM_DATA_DIR>/update-status.json` 为 `{"ok":true,...}`（应用重启后消费）。
- 隔离安装目录 EXE `--version` 为 `0.2.1`。
- SQLite 中 `session_id='acceptance-session'` 的标题仍为 `验收保留标题`。
- 重启的隔离应用窗口标题为 `Agent Title Manager 0.2.1`，无“桌面服务启动失败”弹窗。

## 覆盖的负面场景（Go 单测）

稳定版本比较、降级拒绝、draft/prerelease 跳过、签名错误、清单字段被篡改、哈希错误、网络失败可重试、helper 安装/校验失败时恢复旧版并重启。详见 `internal/update` 与 `internal/desktop` 测试。
