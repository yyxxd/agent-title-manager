# Agent Title Manager

Windows 桌面应用，在本机管理 Codex、Claude、Pi 等 Agent 的自动会话标题。启用对应插件后，应用才开始记录处理结果。概览按 Windows 本地日期与 Agent 展示模型请求、标题回执和已知 Token 用量。未回报用量单独计数。

## 安装与使用

运行 Windows x64 安装程序，从开始菜单打开 Agent Title Manager。应用需要 Microsoft Edge WebView2 Runtime。Node.js 运行时已随安装程序分发。

1. 在「模型与规则」选择 Codex 订阅、Claude Code 或自定义 API。原生来源从本机 CLI 读取可见模型和各模型的思考强度。通过「测试首选来源」验证当前草稿的订阅资格及额度。Codex Fast 单独开关，实际档位见记录。启用回退后，只有 Codex 确认失败且已配置 API 时才改用 API。
2. 在“Agent 插件”启用对应适配器。若发现受支持的旧版自动命名插件，按界面确认接管。Codex 若要求核准 Hook，请在 Codex 中运行 `/hooks`。
3. 若使用 Orca，按需启用同步。处理记录提供失败重试及受保护标题的手动采用操作。

Claude 完成第一轮对话后会在后台生成标题。候选就绪后，发送下一条消息或重新打开该会话时应用。

Claude Code 命名来源使用原版 CLI 自己的原生订阅登录，适用于 Codex、Claude 和 Pi 会话。与用户配置隔离后查不到原生订阅登录时，仍可浏览 CLI 模型目录，连接测试会提示检查登录。

配置自定义 API 时，选择 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 或 Gemini 协议，再填写 Base URL。应用会按协议补全请求地址并显示预览。可用当前草稿获取模型列表并搜索选择，也可手动填写模型 ID。已有 0.1.1 API 配置默认沿用 Chat Completions 和原请求地址。

“概览”优先显示所选日期范围的改名次数和命名 Token，并提供按 Agent 和模型的用量明细。Token 合计为输入加输出，缓存计入输入，缺失用量会标明。“Agent 插件”在对应插件下直接显示简要说明。“记录”每行右侧显示命名状态，以及适用的 Orca 同步状态。

外观默认跟随 Windows 应用主题。在“模型与规则”中也可选浅色或深色，选择会保留。

接管旧插件后，可在“Agent 插件”中对相应插件选择“恢复”，撤销接管。

关闭窗口后应用继续驻留托盘。可通过托盘菜单或“模型与规则”的“退出桌面应用”退出。首次安装时 Windows 登录启动关闭，可在同一页面打开。

升级桌面程序前先从托盘退出。重新打开后，“Agent 插件”会显示可用的随包更新，选择“更新至”即可升级对应内置适配器。

从 0.1.1 升级到 0.1.2 时，可在原安装目录覆盖安装。打开后可在窗口标题或托盘提示中核对版本，Windows“已安装的应用”中也会显示对应版本。

卸载前请在“Agent 插件”停用适配器，并用“恢复”撤销旧版插件接管，再从托盘退出。应用运行中、插件仍启用或旧版接管待恢复时，卸载程序会暂停。卸载成功后保留 `%LOCALAPPDATA%\AgentTitleManager` 中的用户数据。

## Windows x64 构建

准备 Go 1.26 或更新版本、Node.js 22+、pnpm 11、官方 Node.js Windows x64 解压目录，以及 NSIS 3.x。Node.js 解压目录应包含 `node.exe` 和 `LICENSE`。先按 Node.js 发布页附带的 SHA-256 清单验证下载。

```powershell
.\packaging\build.ps1 -NodeDistribution C:\tools\node-win-x64 -MakeNsis C:\tools\nsis\makensis.exe
```

更新应用图标时，替换 `internal/desktop/icon.png` 后运行 `go generate ./cmd/atm`，并提交两个架构的资源文件。打包脚本会检查资源文件是否与源图一致。

脚本执行前端构建和 Go 测试，将桌面可执行文件、bridge、内置插件和 Node.js 组装至 `out\windows-x64-*`，默认生成 `out\AgentTitleManager-0.1.5-windows-x64-setup.exe`。默认版本从 `internal/desktop/version.txt` 读取。当前构建产物未签名。

测试可为桌面应用和 bridge 同时设置绝对路径 `ATM_DATA_DIR`，隔离 SQLite、WebView2 数据及命名管道。验证安装程序时使用独立目录，例如 `setup.exe /S /NoIntegration /D=C:\temp\atm-sandbox`。插件操作仍须使用独立 Agent 配置与会话样本。

## 插件通信

适配器启动 `atm-bridge.exe rpc`，在 stdin 提交一个 UTF-8 JSON 对象，在 stdout 读取一行响应。协议 v1 允许 `event.submit`、`title.claim` 和 `title.ack`。响应为 `{"ok":true,"result":...}` 或 `{"ok":false,"error":"..."}`。桌面未运行、插件未启用或超时后，适配器应继续原生会话。后台可在收到 `ingress_busy:` 时有限重试同一提交。收到 `ingress_pending` 时等待候选，无需重提事件。

社区插件可使用包含 `agent-title-plugin.json` 的本地目录，或指定 GitHub 仓库和完整提交 SHA。

来自原 `oil-codex-title` 插件的代码和命名提示词保留其 MIT 许可和署名。安装包包含 `OIL-CODEX-TITLE-LICENSE.txt` 及插件目录的许可文件。
