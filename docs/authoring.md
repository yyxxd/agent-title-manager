# 开发 Agent Title Manager 插件

一个插件以一个可单独分发的目录发布。发布包根目录包含 UTF-8 编码的 `agent-title-plugin.json`，入口和所需文件使用包内相对路径。独立第三方插件使用 `host: "external"`，管理器登记启用状态，由插件作者在外部宿主接入生命周期。把示例目录复制后，给插件选择一个唯一、稳定的 ID，并在每次桥接调用中使用同一个 `agent`。

```json
{
  "schemaVersion": 1,
  "id": "example",
  "name": "外部宿主标题示例",
  "version": "0.1.0",
  "kind": "agent",
  "host": "external",
  "entry": "index.mjs"
}
```

[最小示例](../plugins/example/index.mjs) 的 `nameSession(host, rpc)` 展示完整接入。外部宿主实现 `read()` 和 `applyIfCurrent(snapshot, title)`。可在宿主结束一轮后调用，并在后续空闲时用同一快照重试领取。示例的 `submit` 命令也可从 stdin 提交当前宿主真实读到的快照。调用形式为 `node index.mjs submit`。

## 桥接协议

执行 `atm-bridge.exe rpc`，把单个 JSON 请求作为 UTF-8 写入 stdin，随后关闭 stdin。stdout 返回一个 UTF-8 JSON 信封。调用方应设置短超时，后台处理做有界等待。桌面退出或插件停用时会话继续。

```json
{
  "v": 1,
  "method": "event.submit",
  "params": {
    "agent": "example",
    "sessionId": "session-42",
    "revision": "turn-18",
    "currentTitle": "",
    "turns": [{ "role": "user", "text": "此处由宿主提供真实文本" }]
  }
}
```

成功信封为 `{"ok":true,"result":{...}}`，失败为 `{"ok":false,"error":"..."}`。插件端只使用以下方法。

| 方法 | 参数及行为 |
| --- | --- |
| `event.submit` | 包含 `agent`、`sessionId`、`revision`、`currentTitle`、`turns`，可带 `project`、`originalGoal`、`confirmationOnly`。会话 ID 用宿主的原生标识，revision 用宿主轮次/分支 ID 或稳定指纹。返回包含 `accepted`，生成在后台进行。只提交近期用户及助手文本，略过工具输出、推理和系统消息。 |
| `title.claim` | 传 `agent`、`sessionId`、`currentTitle`、`revision`，返回 `{candidate:null}` 或 `{candidate:{id,title,baseTitle,revision}}`，可附同版本 `status`。应用前再次核对会话、版本和当前标题，比较后立即写入。 |
| `title.ack` | 传 `agent`、`sessionId`、候选 `id`、读回的 `observedTitle`。宿主读回标题与候选一致后再发送，重复回执沿用同一候选 ID。 |

桥接程序用当前 Windows 用户命名管道。执行程序路径由管理器生成的 `atm-runtime.json` 提供，例如字段 `bridgePath` 指向原生可执行文件的绝对路径。通过 argv 传递固定的 `rpc` 子命令，JSON 走 stdin。插件包排除运行配置、凭据、日志和会话资料。模型和凭据由桌面管理。

`originalGoal` 来自原生会话开头的第一条真实用户消息，无法读取时省略。只有能确认本轮用户输入均为白名单内的纯文本确认语时才发送 `confirmationOnly: true`。图片、引用占位和 Claude 独立附件记录都会留给正常处理。核心还会核验标题和上一次完整基线。

`ingress_pending`、`queued`、`running` 表示仍在处理。`kept`、`failed`、`interrupted`、`stale`、`applied`、`disabled` 使后台立即结束查询。`none` 表示桥接尚未发现该版本的记录。只有 `event.submit` 明确返回 `ingress_busy` 时才针对同一版本有限退避重试。

## 已有标题与 Orca

首次打开已有原生标题时，可发送 `nativeTitleOnly: true`、真实 `currentTitle`、`turns: []` 和经过验证的 Orca 绑定。该事件由核心走原名同步路径。没有 Orca 绑定时无需发送此事件。

使用 Orca 后，先以宿主原生 handle 查询 `orca agent show <原生 handle> --json`，再按 Orca 返回的唯一 ID 反查 `orca agent show orca_session_id:<原始 ID> --json`。校验 Agent 类型、唯一活动位置和注入身份（如有），把原始 ID、已观测标题及原生 handle 分别放到 `orcaSessionId`、`orcaTitle`、`orcaNativeHandle`。Codex 与 Claude 用原生会话 ID 反查。Pi 用 `getSessionFile()` 反查，协议会话键仍然用 `getSessionId()`。当前 Orca CLI 的显示结果没有标题来源，适配器上报 `orcaTitleOrigin: "unknown"`，空标题以空字符串传递。非空且来源未知的现有标题可由桌面明确采用。查不到唯一映射时省略 Orca 字段，Agent 的原生命名仍可工作。

## 第一方发布目录

| 宿主 | 管理器安装入口 | 运行条件 |
| --- | --- | --- |
| Codex | `codex/index.mjs stop` 注册异步 Stop。 `codex/index.mjs start` 注册异步 SessionStart，用于已有标题和恢复候选。 | Node 22+ 与 Codex 原生 CLI。可通过 `atm-runtime.json` 的 `codexPath` 设置 `codex.exe` 绝对路径，缺省从 PATH 的原生或 npm 安装定位。Stop 通过 App Server 复核最新已完成的 turn，关闭 App Server 等待候选，再打开核对并写回。 |
| Claude Code | `claude/index.mjs stop` 注册异步 Stop，`prompt` 注册 UserPromptSubmit，`start` 注册 SessionStart。 | Node 22+。Stop 提交已落盘版本，后续 UserPromptSubmit 或 SessionStart 领取。原生 Hook 接受 `sessionTitle` 后由短时观察进程读取 transcript 或 sidecar 确认。 |
| Pi | 将 `pi/index.js` 的绝对路径登记到 Pi 的 `extensions`，一并安装包内依赖。 | 当前 Pi 扩展环境。使用 `agent_settled` 异步提交，在当前分支仍空闲时设置原生标题并显示本地通知。 |
| 外部 | `example/index.mjs` 提供宿主接入示例，由外部宿主触发。 | Node 22+ 用于此例。第三方可使用任意支持 stdin/stdout 的语言。 |

Windows 桌面发行可以附带 Node 22+，由管理器在 Codex、Claude 的命令中使用 Node 与脚本的绝对路径。Pi 由宿主自己加载，不额外启动 Node。管理器安装和撤销其登记的 Hook/扩展项。接管旧插件的选项由桌面统一管理。

Codex 和 Pi 每次后台等待最多八分钟。之后重新打开同一会话，适配器会按仍匹配的最新版本重新领取，沿用原有生成任务。Claude 每次前台 Hook 只领取一次，尚在处理时由后续可用 Hook 再领取。

第一方发布目录自包含。修改 `plugins/shared/` 后运行 `node plugins/sync-support.mjs` 刷新副本。用 `node plugins/sync-support.mjs --check` 检查一致性，用 `node --test plugins/tests/*.test.mjs` 运行隔离适配器测试。

设置 `ATM_TEST_CODEX_EXE` 指向原生 Codex CLI、`ATM_TEST_CLAUDE_EXE` 指向原生 Claude Code CLI、`ATM_TEST_PI_CLI` 指向 Pi 的 JavaScript CLI，可在同一命令中运行真实宿主的离线冒烟测试。Claude 测试用本机回环端点和桥接测试替身，验证宿主接受 Hook 标题并写入后才回执。测试从临时目录加载用户配置，不访问个人会话或模型凭据。
