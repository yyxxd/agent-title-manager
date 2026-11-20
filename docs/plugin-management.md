# Windows 插件管理

「支持的 Agent」管理 Codex、Claude Code、Pi、Antigravity 和社区适配器。「工作台集成」管理 Orca 标题同步。各项默认关闭，开关延续原有的 `plugin-manager/state.json` 记录。升级继续使用已有的 `engine.sqlite3` 会话和历史记录。

## 安装与启用

| 插件 | 启用时的操作 | 启用后的步骤 |
| --- | --- | --- |
| Codex | 在 `CODEX_HOME` 或 `~/.codex` 的 `hooks.json` 添加 `SessionStart` 和 `Stop` | 按提示在 `/hooks` 审核和信任，随后打开会话或完成一轮对话。登记与当前版本匹配的 Hook 实际运行后，Agent 页解除「需操作」。 [Codex Hook 文档](https://developers.openai.com/codex/hooks) |
| Claude Code | 在 `CLAUDE_CONFIG_DIR` 或 `~/.claude` 的 `settings.json` 添加 `SessionStart`、`UserPromptSubmit` 和 `Stop` | 重启宿主。 [Claude Code Hook 文档](https://code.claude.com/docs/en/hooks) |
| Antigravity | 在 `ANTIGRAVITY_CONFIG_DIR` 或 `~/.gemini/config` 的 `hooks.json` 登记 CLI 的 `SessionStart`、`PreInvocation`、`Stop`。桌面端由应用观察本地完成事件 | 重启 agy 加载 Hook。CLI 可使用 `ANTIGRAVITY_DATA_DIR` 指定数据根。 |
| Pi | 在 `PI_CODING_AGENT_DIR` 或 `~/.pi/agent` 的 `settings.json` 添加适配器入口到 `extensions` | 重启宿主。 [Pi 扩展文档](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/extensions.md) |
| Orca | 开启桌面的标题同步 | 在 Orca 设置中关闭 **Name chats automatically**，避免两个应用同时改名。 |

随附适配器位于桌面发布目录的 `plugins/codex`、`plugins/claude`、`plugins/pi` 和 `plugins/antigravity`。需要独立 Node 进程的 Hook 优先使用随附的 `node.exe`，也可从 `PATH` 查找。Pi 由宿主加载扩展。适配器通过同目录的 `atm-runtime.json` 获得原生桥接程序路径。

如果新版桌面应用附带更高版本的内置适配器，启动时会自动更新已安装的随附适配器，并保留开关状态。登记成功后会清理已退役包中的 Orca 辅助脚本。自动更新失败时可从插件页重试。Codex 更新后需在 `/hooks` 审核新 Hook。

Codex 的信任由 Codex 管理。版本更新或重新登记后，桌面等待新的 Hook 运行证明。检测到登记缺失、配置目录变化或适配器损坏时，Agent 页提示停用后重新启用。

**停用**撤下管理器登记的入口。未完成的登记变更可在 Agent 页回滚，遇到宿主设置冲突时保留现有内容。登记和未完成操作保存在桌面数据目录的 `plugin-manager/state.json`，其中可能包含宿主原配置，应保存在本机。

卸载前需停用已启用的适配器和集成，再退出桌面应用。卸载检查只读，未完成的变更或无法读取的注册状态会阻止卸载。

管理器作用于桌面进程使用的用户配置目录。项目级 Hook、企业托管设置、其它配置目录及已有第三方插件由用户在对应宿主管理。停用后重启宿主可重新加载设置。

## 独立插件

从插件页选择本地目录或公开 GitHub 仓库。GitHub 安装需要 `owner/repo` 和 40 位提交 SHA。本地使用绝对目录路径。清单位于所选目录或 GitHub 仓库根目录，文件名为 `agent-title-plugin.json`。

```json
{
  "schemaVersion": 1,
  "id": "my-adapter",
  "name": "我的适配器",
  "version": "1.0.0",
  "kind": "agent",
  "host": "external",
  "entry": "index.mjs"
}
```

使用小写字母开头的唯一 ID，其余字符限小写字母、数字和连字符，最长 63 字符。使用 SemVer 版本和以正斜杠分隔的包内入口。安装包限制为 64 MiB、4,096 项，路径必须兼容 Windows。管理器通过静态解析和文件复制安装，链接、路径逃逸、保留设备名称和伪造的运行配置会导致安装失败。使用其他包的受信任代码前，由运行者审核其内容。

安装完成后，显式启用该 ID，再用自己的运行时启动入口。启用会创建 `atm-runtime.json`，社区插件条目返回安装后的 `entryPath`。桥接只允许已启用的 ID 调用 `event.submit`、`title.claim`、`title.ack`。模型凭据留在桌面服务。更新前先停用插件，再选择同一 ID 的发行包。

## 桌面管理接口

| 方法 | 参数 | 返回值 |
| --- | --- | --- |
| `plugins.list` | 无 | `{"items":[Item]}`，可附启动时的 `autoUpdate` |
| `plugins.install` | `{source:"local",path}` 或 `{source:"github",repo,ref}` | `Item` |
| `plugins.update` | 与安装相同，另加 `id`；内置更新使用 `{id,source:"bundled"}` | `Item` |
| `plugins.enable` | `{id}` | `Item` |
| `plugins.disable` | `{id}` | `Item` |

`Item` 包含 `id`、`name`、`version`、`enabled`、`kind`、`status`，可选字段为 `detail`、`entryPath`、`availableVersion`。`kind` 为 `builtin`（随附 Agent）、`community`（社区 Agent）或 `integration`（工作台集成）。工作台集成使用主程序版本，单独显示于 Agent 计数之外。`status` 为 `disabled`、`enabled`、`action_required`、`unavailable` 或 `recovery_required`。Codex 的 `enabled=true` 表示登记已写入，运行证明由实际 Hook 报告。

## Orca 标题核对

Agent 适配器上报原生会话、版本、回合与标题。Pi 用 `nativeHandle` 上报可核验的原生会话文件路径，Antigravity CLI 用同字段上报原生标识。CLI Hook 可透传 `hostSessionHint` 供反查。Pi 和 Antigravity CLI 可同步到 Orca 终端标签。打开会话时上报的原生标题仅供同步。Orca 集成关闭时，桌面服务忽略这类观测，停止 Orca Chat 观察，也跳过历史记录的 Orca 同步请求。

集成开启时，桌面服务使用官方 CLI 反查本地会话。可用 `ORCA_CLI_COMMAND` 指定可执行文件。宿主提示必须与反查结果一致。终端只接受可唯一确认的单 pane 标签页；聊天通过本地会话 ID 核对。绑定延迟出现时，主程序在原生改名确认后补查，并将结果写入同一条记录。

已绑定聊天的标题来源从 `ORCA_USER_DATA_PATH` 或 `%APPDATA%/Orca` 下的 SQLite 数据库只读核对。未知来源按保护处理，需用户显式接管。每次写入前重新核验 Orca 位置、原生标题与当前目标。Codex 使用 App Server `thread/read`，Claude Code 读取会话元数据，Pi 读取已校验会话文件的 `session_info`，Antigravity 读取注解。主程序执行 `orca chat rename` 或 `orca terminal rename` 后，通过 CLI 确认结果。

Antigravity Desktop 观察器由 Antigravity 开关控制。Orca Chat 观察器仅在 Orca 和 Antigravity 同时开启时运行，只处理本机已成功结束的 Antigravity ACP 回合。观察器校验记录中的 `accountHome` 和受控的 `antigravity-acp` 目录，复用 Antigravity 原生转录和标题读写，并跳过用户手动命名的聊天。
