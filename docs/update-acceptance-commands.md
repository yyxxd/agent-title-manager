# 应用内更新隔离验收命令（供人工接手）

以下全部在隔离工作树内运行。**不要**设置正式安装目录、正式 `LOCALAPPDATA\AgentTitleManager` 或真实 GitHub Release；所有进程必须带 `ATM_DATA_DIR`。密钥是临时测试密钥，用后丢弃，不提交、不配置 GitHub Secret。

## 1. 临时测试密钥

```powershell
cd C:\Users\yy\orca\workspaces\agent-title-manager\settings-control-polish
$seed = -join ((1..32) | ForEach-Object { '{0:x2}' -f (Get-Random -Max 256) })   # 仅本地内存
$seed | Set-Content -NoNewline -Encoding ascii .tools\test-seed.txt
$env:ATM_SIGNING_SEED = Get-Content -Raw .tools\test-seed.txt
go run ./internal/releasekey > .tools\test-public.txt
```

`.tools/` 已在 `.git/info/exclude` 中排除。

## 2. 隔离构建两个版本（0.2.0 起点、0.2.1 目标）

Node 官方分发须先校验 SHA-256，解压到 `.tools\node-dist\node-v22.14.0-win-x64`（含 `node.exe` 与 `LICENSE`）。NSIS 3.13 位于 `.tools\nsis\makensis.exe`。

```powershell
$pub = (Get-Content -Raw .tools\test-public.txt).Trim()
.\packaging\build.ps1 -NodeDistribution .tools\node-dist\node-v22.14.0-win-x64 -MakeNsis .tools\nsis\makensis.exe -Version 0.2.0 -UpdatePublicKey $pub
.\packaging\build.ps1 -NodeDistribution .tools\node-dist\node-v22.14.0-win-x64 -MakeNsis .tools\nsis\makensis.exe -Version 0.2.1 -UpdatePublicKey $pub
```

构建会注入公钥到**桌面 exe 与 helper exe**；helper 安装前会用内置公钥重新验签。

## 3. 隔离安装与真实 schema 数据

```powershell
$data = 'C:\Users\yy\orca\workspaces\agent-title-manager\settings-control-polish\.tools\acceptance\data'
$install = 'C:\Users\yy\orca\workspaces\agent-title-manager\settings-control-polish\.tools\acceptance\install with space'   # 含空格，验证自定义路径
$env:ATM_DATA_DIR = $data
# 每轮开始前只停止指向隔离目录的进程：
Get-CimInstance Win32_Process -Filter "Name='AgentTitleManager.exe'" |
  Where-Object { $_.ExecutablePath -like '*settings-control-polish*' } |
  ForEach-Object { Stop-Process -Id $_.ProcessId -Force }
# 安装 0.2.0（/D= 必须最后且不带引号，路径含空格也可）：
Start-Process out\AgentTitleManager-0.2.0-windows-x64-setup.exe -ArgumentList @('/S','/NoIntegration',"/D=$install") -Wait
# 用真实引擎 schema 建库并写入标记数据：
go run ./tests/update-fixture/seed/main.go -data $data
```

## 4. fixture 与签名清单

启动隔离 HTTP 服务（只监听 127.0.0.1）：

```powershell
node tests/update-fixture/server.mjs .tools\fixture-root 18765
```

`fixture-root` 放 0.2.1 安装包与签名清单。签名清单用临时私钥生成，说明来自实际文本：

```powershell
$env:ATM_SIGNING_KEY = Get-Content -Raw .tools\test-seed.txt
$env:ATM_RELEASE_NOTES = '0.2.1 隔离验收：修复与改进'
$sha = (Get-FileHash -Algorithm SHA256 -LiteralPath out\AgentTitleManager-0.2.1-windows-x64-setup.exe).Hash.ToLowerInvariant()
$size = (Get-Item out\AgentTitleManager-0.2.1-windows-x64-setup.exe).Length
go run ./cmd/atm-sign -version 0.2.1 -file 'AgentTitleManager-0.2.1-windows-x64-setup.exe' -size $size -sha256 $sha -output .tools\fixture-root\update-manifest.json
Copy-Item out\AgentTitleManager-0.2.1-windows-x64-setup.exe .tools\fixture-root\
```

## 5. 真实 Wails UI 链路（不用 prepare）

以隔离环境启动 0.2.0 桌面应用，**所有子进程继承同一 `ATM_DATA_DIR`**：

```powershell
$env:ATM_DATA_DIR = $data
$env:ATM_UPDATE_URL = 'http://127.0.0.1:18765/release'
$env:ATM_UPDATE_ISOLATED = '1'
$env:ATM_INSTALL_DIR = $install
& "$install\AgentTitleManager.exe"
```

在 UI：模型与规则 → 软件更新 → 检查更新 → 下载并安装 → 确认。然后观察：

- 进度条出现，下载完成自动退出；`<data>\update-helper.log` 记录 `verify` 后 `result ok=true elapsed=... message=已更新至 0.2.1`。
- `update-status.json` 保留到 UI 拉取后消费；重启后从任意页面都能看到结果 Toast。
- `$install\AgentTitleManager.exe --version` 为 `0.2.1`，路径含空格仍正确覆盖。
- SQLite 标记 session 标题仍为“验收保留标题”；`PRAGMA integrity_check` 为 `ok`。
- 失败实测：把 fixture 包哈希改坏后重复流程，界面应报错、进度收起、可再次点击；安装目录版本仍为旧版，`update-helper.log` 如实记录失败原因。

## 自动化已覆盖（无需实机）

`go test ./...`、`pnpm build`、`impeccable detect`：全部通过。单元测试覆盖重新验签、篡改清单/包/版本/目标、等待失败、备份失败、回滚白名单、回滚失败不重启、注册表版本钩子、后台状态推送与状态文件保留。
