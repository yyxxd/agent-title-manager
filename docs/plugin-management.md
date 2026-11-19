# Windows 插件管理

插件页管理 Codex、Claude Code、Pi、Orca 同步以及已安装的独立插件。默认全部关闭。启用 Agent 插件后，桌面会注册宿主 Hook 或扩展，并开始接受该 Agent 的桥接事件。启用 Orca 只打开标题同步。

## 安装与接管

| 插件 | 启用时的操作 | 启用后的步骤 |
| --- | --- | --- |
| Codex | 在 `CODEX_HOME` 或 `~/.codex` 的 `hooks.json` 添加 `SessionStart` 和 `Stop` | 首次使用或 Hook 变更后，在 Codex CLI 的 `/hooks` 中审核和信任；重启宿主后再次查看。 [Codex Hook 文档](https://developers.openai.com/codex/hooks) |
| Claude Code | 在 `CLAUDE_CONFIG_DIR` 或 `~/.claude` 的 `settings.json` 添加 `SessionStart`、`UserPromptSubmit` 和 `Stop` | 重启宿主。 [Claude Code Hook 文档](https://code.claude.com/docs/en/hooks) |
| Pi | 在 `PI_CODING_AGENT_DIR` 或 `~/.pi/agent` 的 `settings.json` 添加适配器入口到 `extensions` | 重启宿主。 [Pi 扩展文档](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/extensions.md) |
| Orca | 开启桌面的标题同步 | 在 Orca 设置中关闭 **Name chats automatically**，避免两个应用同时改名。 |

随附适配器位于桌面发布目录的 `plugins/codex`、`plugins/claude` 和 `plugins/pi`。Codex 和 Claude 需要 Node 22 或更新版。管理器优先选择随附的 `node.exe`，并可从 `PATH` 查找。适配器通过同目录的 `atm-runtime.json` 获得原生桥接程序路径。

如果新版桌面应用附带更高版本的内置适配器，插件页会显示可用版本。选择更新后，管理器替换现有注册并保留启用状态与接管备份。Codex 更新后需在 `/hooks` 审核新 Hook。

若插件页检测到支持接管的旧版，需明确选择接管。支持已知市场 ID 的 `oil-codex-title`、包缓存中的同款 Codex 停止 Hook、Claude 用户配置中的 `session-namer/namer.py` 以及 Pi 用户全局的 `extensions/auto-name`。Pi 的旧目录会连同数据移动到同级用户配置范围内的 `.atm-disabled-auto-name`。Codex 的信任记录由 Codex 管理。

**停用**撤下自己注册的入口，保留接管记录。**恢复**撤下现有入口并还原旧插件，或将独立插件回退到上一发行版本。检测到相关设置冲突时，恢复会停下来保留现有文件。处理冲突后可重试。备份和未完成操作保存在桌面数据目录的 `plugin-manager/state.json`。文件中可能存在宿主原配置，应保存在本机。

卸载前需停用已启用的插件，并恢复被接管的旧宿主插件。卸载检查只读；未完成的变更或无法读取的注册状态会阻止卸载。

管理器作用于桌面进程使用的用户配置目录。项目级 Hook、企业托管设置、其他配置目录及自定义旧插件由用户在对应宿主中管理。停用或恢复后重启宿主，使已运行的旧进程重新加载设置。旧插件迁移参考了 Agent Session Namer 及其上游 MIT 项目。 [Agent Session Namer](https://github.com/yyxxd/agent-session-namer) · [oil-oil/oil-codex-title](https://github.com/oil-oil/oil-codex-title)

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

安装完成后，显式启用该 ID，再用自己的运行时启动入口。启用会创建 `atm-runtime.json`，社区插件条目返回安装后的 `entryPath`。桥接只允许已启用的 ID 调用 `event.submit`、`title.claim`、`title.ack`。模型凭据留在桌面服务。更新前先停用插件，再选择同一 ID 的不同版本；需要时可恢复上一版本。

## 桌面管理接口

| 方法 | 参数 | 返回值 |
| --- | --- | --- |
| `plugins.list` | 无 | `{"items":[Item]}` |
| `plugins.install` | `{source:"local",path}` 或 `{source:"github",repo,ref}` | `Item` |
| `plugins.update` | 与安装相同，另加 `id`；内置更新使用 `{id,source:"bundled"}` | `Item` |
| `plugins.enable` | `{id,takeover?:boolean}` | `Item` |
| `plugins.disable` | `{id}` | `Item` |
| `plugins.restore` | `{id}` | `Item` |

`Item` 包含 `id`、`name`、`version`、`enabled`、`kind`、`status`，可选字段为 `detail`、`takeoverRequired`、`canRestore`、`entryPath`、`availableVersion`。 `kind` 为 `builtin` 或 `community`；`status` 为 `disabled`、`enabled`、`action_required`、`unavailable` 或 `recovery_required`。Codex 的 `enabled=true` 代表注册已写入，实际信任状态由宿主决定。

## Orca 标题核对

使用官方 CLI 执行本机操作。可通过 `ORCA_CLI_COMMAND` 指定可执行文件，也可使用已安装的 Windows CLI。Codex、Claude 和 Pi 先用原生会话标识反查 Orca，再用 Orca ID 核实唯一的本机聊天和宿主。Pi 标识使用用户 `sessions` 目录内的会话文件，且首行 ID 必须匹配。远端、终端、重复位置和第三方自报绑定会跳过。

首次识别标题来源时，以 SQLite `mode=ro` 读取 `ORCA_USER_DATA_PATH` 或 `%APPDATA%/Orca` 中已绑定会话的名称元数据。对版本 2 的记录，两个名称字段都缺省且显示确为宿主默认占位名时可判为自动。记录标为 `user` 且名称与实时显示一致时判为手动。未知版本、无记录、库不可读或者来源不明时，应由用户显式接管。

每次写入（包括重试和采用）都比较 Orca 的身份与旧标题，并在最后一步重新核验绑定、读取原生标题。Codex 用 App Server `thread/read`，Claude 使用适配器的标题元数据读回，Pi 读取已绑定会话文件的最近 `session_info`。原生名已改变时，旧任务过期；读不到时保留重试机会。匹配后才执行 `orca chat rename` 并从 CLI 回读。当前宿主读取和 Orca 改名是独立操作，二者之间仍存在竞态窗口。

| Antigravity | \ANTIGRAVITY_CONFIG_DIR\ 下的 \hooks.json\ 命名钩子组 | \SessionStart\、\PreInvocation\、\Stop\，数据目录可由 \ANTIGRAVITY_DATA_DIR\ 覆盖 |
