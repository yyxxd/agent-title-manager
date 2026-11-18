# Windows 安装后验收

在 Windows 11 x64 的可见桌面和交互式 PowerShell 中运行。使用发布负责人指定的最终候选包及其 SHA256，系统需已安装 WebView2 Runtime、Node.js 22+ 和原生 `codex.exe`。Codex 原生会话只通过 App Server 创建、改名和读回，使用空白宿主配置即可，无需登录。

```powershell
node tests/acceptance/windows-installed.mjs '<候选安装包.exe>' '<指定的 SHA256>' '<原生 codex.exe>'
```

脚本在安装前后校验同一候选包的 SHA256。安装使用 `/NoIntegration` 和 TEMP 下的新目录。桌面数据和命名管道由 `ATM_DATA_DIR` 隔离，Codex、Claude、Pi 配置也使用 TEMP。Windows 用户、系统组件和桌面仍共用。没有可用的 Windows Sandbox、Hyper-V 或其他 VM 时，记录为“同一 Windows 用户、TEMP 配置及命名管道隔离”。

按终端提示通过已安装的桌面 UI 完成操作，逐项记录所见结果。

| 阶段 | 验收点 |
| --- | --- |
| 首次安装 | 社区列表为空。内置 Codex 直接启用，无旧版本接管提示。脚本检查隔离配置中各有一个 `SessionStart`、`Stop` Hook，停用后空白配置恢复。 |
| 安装并启用 v1 | 选择脚本生成的本地目录。在桌面设置只填脚本给出的 `127.0.0.1` 模型地址与模型名，不填密钥或使用“测试连接”。独立插件使用安装生成的 runtime 文件调用已安装 bridge。脚本经 Codex App Server 写入并读回标题，随后发送 ACK。核对 UI 中一条已应用记录、一次模型请求以及 `19/5/4` 个输入、输出、缓存 token。 |
| 同 ID 升级 | 停用 v1，选择 v2 本地目录更新并启用。列表中同一个插件只出现一次且显示 v2.0.0，原生旧标题仍可读取。 |
| 停用与保留 | 停用 v2 后，脚本用新的原生会话提交事件并断言 bridge 拒绝，回环模型调用计数仍为 1。刷新 UI 确认记录与累计计数保持，脚本重启 Codex App Server 并再次读回旧标题。 |

记录安装包 SHA256、Windows 和 Codex 版本、实际隔离方式、每步观察及脚本最终结果。回环模型返回固定的合成数据，实际订阅用量和 tier 需要单独的 Codex CLI JSONL 证据。脚本保留 TEMP 安装与测试目录，便于查错。报告中只保留脱敏结果，不附原始会话内容、本机路径、账户或凭据。
