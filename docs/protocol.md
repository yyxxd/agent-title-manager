# Agent Title Manager 本机协议 v1

桌面与本机 bridge 使用 UTF-8 JSON。命名进程用 SQLite 持久化会话、修订、候选、调用记录和 Orca 同步状态。事件入口只入队，Agent 不等待模型。

## 传输与授权

`atm-bridge.exe rpc` 从 stdin 读一个请求，stdout 只输出一个响应对象，诊断写 stderr。Windows 桌面通过当前用户命名管道提供服务。

```json
{"v":1,"method":"title.claim","params":{"agent":"codex","sessionId":"session-1","revision":"rev-1","currentTitle":""}}
```

```json
{"ok":true,"result":{"candidate":null,"status":"none"}}
```

```json
{"ok":false,"error":"会话键、版本或标题无效"}
```

bridge 只允许 `event.submit`、`title.claim`、`title.ack`。宿主在分发前验证插件已启用且有权使用 `params.agent`，在退出或桌面不可用时让原生 Hook 快速放行。第三方插件的 Orca 映射或标题来源由宿主独立验证，无法证明归属时宿主剥离 Orca 字段。设置、凭据、诊断、统计和插件管理只走可信 UI。插件收到的是标题候选，API 密钥始终由后端保管。

Go 宿主创建 `engine.Open(dataDir, syncer)`，通过 `Engine.Handle(ctx, method, params)` 分发，退出时调用 `Close()`。同一个数据目录只允许一个打开的 Engine。`plugins.*` 由宿主交给 `pluginmgr.Manager.Handle`。Engine 的 `Handle` 同时提供可信方法，宿主负责 bridge 白名单。

桌面可通过 `engine.Open(dataDir, syncer, isEnabled)` 提供线程安全的 `func(context.Context, string) bool`。引擎在原生和备用模型各自开始前检查当前插件启用状态，包括重启恢复的排队事件。停用时不开始新调用，已经开始的请求照常记账。可信 UI 发起的连接测试独立于 Agent 启用状态。

## Agent 事件与回执

### `event.submit`

```json
{
  "agent": "codex",
  "sessionId": "native-session-1",
  "revision": "stable-turn-revision",
  "currentTitle": "",
  "titleOrigin": "auto",
  "project": "demo",
  "originalGoal": "修复构建路径",
  "orcaSessionId": "raw-orca-session-1",
  "orcaTitle": "",
  "turns": [
    {"role": "user", "text": "修复构建路径"},
    {"role": "assistant", "text": "我会检查构建配置。"}
  ]
}
```

返回 `{"accepted":true}`。会话键是 `(agent, sessionId)`。`agent` 允许 `codex`、`claude`、`pi` 及通过宿主注册的 ID。ID 格式为 `[a-z][a-z0-9._-]{0,63}`。`revision` 必须是来源内容的稳定版本，重试须复用。提供最近最多 24 条对话，角色为 `user` 或 `assistant`。模型只收到最近最多 10 条与限定字数的对话，当前缺少有效用户目标时保留原标题。

可选的 `originalGoal` 仅用于原生历史可以确认的起始用户目标，引擎最多取 800 个 Unicode 码点。省略时模型仅见近期内容。绝对 `project` 路径在本地规范化并计算项目哈希，外发提示仅包括通过校验的目录末级名称。已知同项目的标题及活跃候选参与重名检查，撞名时保留原标题。同名末级目录归属不同路径时分别处理。

`confirmationOnly:true` 仅在新用户消息明确是完整纯文本确认且无附件时提供。核心核对当前已受管标题、上一已处理版本的完整对话前缀、项目及已知原始目标，再检查新增用户消息是否为短确认。最多连续免调用两次。近期窗口滚动或基线内容变化时按常规处理。

`titleOrigin` 可选 `auto`、`manual`、`unknown`，默认 `unknown`。只有宿主能证明标题由 Agent 自动创建时传 `auto`；空名可进入命名。初次非空且来源未知、明确手动名，或运行中观察到无法解释的标题变化时保护原生名，即使其格式像自动名。新版本使旧候选失效。已见过的同一版本重新提交只返回原有接受结果，不再次调用模型。修订值不需要可排序，适配器应提交最新观察到的版本，领取时再核对本地版本。

Orca 绑定字段可选。`orcaSessionId` 是 Orca 原始 ID。Pi 适配器可另报 `orcaNativeHandle`，bridge 仅在验证绑定后透传该字段，供宿主同步写前定位原生会话。省略 `orcaTitle` 表示未观察到其原标题，明确空字符串表示已确认当时为空。可选的 `orcaTitleOrigin` 与原生来源使用同一枚举，缺少可信来源省略。当前 Orca 列表接口的标题不携带来源。只有已证实自动名、未标为手动的空名、与该会话上次成功写入一致的 Orca 标题才允许自动替换。Orca 自己标记为手动的管理器写入回声仍由上次已读回的标题识别。首次未知的非空 Orca 标题及其它手动标题由 UI 选择之后处理。同步始终以记录中接收到的 Orca 原标题为预期值。

### 已有原生标题补同步

```json
{
  "agent": "claude",
  "sessionId": "native-session-2",
  "revision": "stable-native-and-orca-observation",
  "currentTitle": "已有原生名",
  "nativeTitleOnly": true,
  "orcaSessionId": "raw-orca-session-2",
  "orcaTitle": "Orca 中的旧名字",
  "turns": []
}
```

该模式要求宿主已确认非空原生标题和完整 Orca 绑定。只尝试同步现有原生名，未知 Orca 名触发保护状态。它与普通事件分开去重，无模型调用、无改名计数，也不替换普通事件的修订。建议由原生标题及已观测 Orca 标题组成稳定修订。

### `title.claim`

```json
{"agent":"codex","sessionId":"native-session-1","currentTitle":"","revision":"stable-turn-revision"}
```

```json
{"candidate":{"id":"candidate-id","title":"🧩 构建｜修复路径","baseTitle":"","revision":"stable-turn-revision"},"status":"claimed"}
```

`revision` 可省略，有来源版本时应提供。没有可用候选时 `candidate` 为 `null`，可选的 `status` 对应请求版本，未知记录为 `none`，已过期版本为 `stale`。桌面接收入队期间可返回 `ingress_pending`。适配器可继续等待 `queued`、`running` 或已交付宿主待读回的 `claimed`，在 `kept`、`failed`、`interrupted`、`stale`、`applied`、`disabled` 时结束。相同原始标题和当前版本可重复领取同一个 ID。宿主应用前重新核对当前版本和标题。候选本身不会触发同步或改名计数。

### `title.ack`

```json
{"agent":"codex","sessionId":"native-session-1","id":"candidate-id","observedTitle":"🧩 构建｜修复路径"}
```

宿主写入并从原生会话读回后再提交。Claude Hook 返回 `sessionTitle` 表示已交给宿主，须随后观察一致的原生标题再确认。有效回执返回 `{"accepted":true}`，重复回执为幂等成功，无效或过期回执返回 `{"accepted":false}`。新版本明确观察到已领取候选的标题时，旧回执仍可确认这一已发生的写入。核心此时才计一次原生改名，并排队做 Orca 同步。

## 可信 UI 方法

### 设置与连接测试

`settings.get {}` 返回以下默认值。`settings.save` 接受同名可写字段的部分更新，并返回更新后的同形设置。`apiKey` 只写，省略时保留、空字符串清除。更改 API 地址时须同时填写或清除旧地址的密钥。Windows 使用当前用户 DPAPI 加密，`hasApiKey` 仅表示已包含密钥。允许 HTTPS，以及回环地址的 HTTP，API 请求不跟随重定向。

```json
{
  "provider": "codex",
  "codexModel": "gpt-6-luna",
  "fast": true,
  "apiProtocol": "chat",
  "apiBaseUrl": "",
  "apiModel": "",
  "hasApiKey": false,
  "fallbackEnabled": false,
  "maxTitleLength": 15
}
```

`provider` 为 `codex`、`claude` 或 `api`。`maxTitleLength` 只接受 `15`（简短，默认）或 `30`（详细），以 Unicode 码点计算，包含 emoji、空格和全角分隔符。旧设置按最近的预设迁移，22 及以下取 15，23 及以上取 30。历史记录保留当时的上限。`fast` 打开时 Codex 请求 `priority`，实际是否生效取服务返回的证据。Codex 使用原生 CLI 的 ChatGPT 登录状态及临时 exec，通常由 npm Windows 原生可执行文件提供。支持 CLI 默认的文件认证存储；管理器通过 CLI 检查账号类型，从不打开认证文件。使用低推理档，忽略用户配置，关闭 Hook、apps、插件、shell 和多 Agent 等能力，模型调用不持久化原生会话。

`apiProtocol` 为 `chat`、`responses`、`anthropic` 或 `gemini`，同时用于 API 命名、连接测试及备用服务。每次生成发送一个非流式 JSON 请求。保存时保留用户填写的地址路径。新地址只有主机时，Chat、Responses 和 Anthropic 使用 `/v1`，Gemini 使用 `/v1beta`。Chat、Responses 和 Gemini 保留已有的代理前缀。Anthropic 的无版本代理前缀追加 `/v1`，也可粘贴完整端点精确指定路径。0.1.1 已保存的地址默认使用 `chat` 并继续沿用旧路径规则，直到改动地址或协议。

`providers.endpoints {"apiProtocol":"gemini","apiBaseUrl":"https://api.example.com","apiModel":"demo"}` 在本地解析并返回 `{"generationUrl":"https://api.example.com/v1beta/models/demo:generateContent","modelsUrl":"https://api.example.com/v1beta/models"}`。未填写 Gemini 模型时，生成路径用 `{model}` 占位。若已粘贴完整生成地址，预览可以从中显示模型。完整生成地址的后缀与所选协议冲突时，程序提示切换协议或修改 API 地址。

`providers.models {"apiProtocol":"anthropic","apiBaseUrl":"https://api.example.com"}` 向本次指定地址发 GET，返回 `{"items":[{"id":"demo","name":"Demo"}]}`，其中 `name` 可省略。支持供应商返回的分页游标。列表请求有独立超时，未保存的地址也可使用。若供应商不提供模型列表，可直接填写模型 ID。此调用不计入命名用量。

`providers.subscriptionModels {"provider":"codex"}` 读取原生 CLI 可见模型。响应 `items` 每项包含精确 `id`、`efforts`，还可提供 `name`、`resolvedModel`、`defaultEffort`。Claude 在隔离模式下未检测到原生订阅登录时会返回 `notice`。目录读取只使用 CLI 元数据，模型使用资格与额度由连接测试确认。

`providers.test` 发送合成连接测试请求，无真实会话历史，可能产生模型用量。可测试已保存值或覆盖本次参数。示例 `{"provider":"api","apiProtocol":"responses","apiBaseUrl":"https://api.example.com/v1","apiModel":"demo"}`。可另传 `apiKey`，Codex 可另传 `codexModel`、`fast`。成功返回 `{"provider":"api","model":"demo","ok":true}`，失败返回外层 RPC 错误。不保存覆盖字段，不走备用服务。

两种联网调用均可传入本次使用的 `apiKey`。省略时，仅在地址与已保存地址一致的情况下使用 DPAPI 密钥。传空字符串时以无密钥方式调用，保存的密钥仍可用于后续请求。Chat 和 Responses 使用 Bearer 鉴权，Anthropic 使用 `x-api-key` 和 `anthropic-version`，Gemini 使用 `x-goog-api-key`。

备用服务默认关闭。开启后，仅在 CLI 明确不可用、没有 ChatGPT 登录或回传明确拒绝且无执行证据时调用已配置 API。超时、断开或含糊错误不会触发备用调用，也不会自动重试同修订事件。核心收到 CLI 可观测的含糊重连事件时会终止临时进程。同步错误只影响同步队列。

### 日统计和记录

`stats.daily {"date":"2026-11-18"}` 以运行 Windows 主机的本地日历日为准，省略 `date` 时取今天。`since` 是本数据库开始记账的时间。模型调用统计由开始请求的时间归属，原生改名以确认回执时间归属。连接测试计入 `agent:"test"`，且计入 `testAttempts`。无回执同步和历史会话不增加改名计数。`byModel` 按 `provider` 和 `model` 汇总调用与用量。

每类 Token 单独汇总已报告的数值，全部缺报时为 `null`。明确报告零时保留 `0`。对应的 `inputKnown`、`outputKnown`、`cachedKnown` 是各自已报告的次数，缺报次数等于 `attempts` 减相应已知次数。`unknownUsage` 统计至少缺一个维度的调用。

Anthropic 输入计入已报告的缓存创建和读取 Token，缓存列记录读取 Token。Gemini 输入字段已包含缓存，输出计入独立报告的思考 Token。缓存字段缺报时仍记为 `null`。

```json
{
  "date": "2026-11-18",
  "since": "2026-11-18T09:00:00+08:00",
  "attempts": 1,
  "testAttempts": 0,
  "renames": 1,
  "kept": 0,
  "inputTokens": 50,
  "outputTokens": 8,
  "cachedTokens": null,
  "inputKnown": 1,
  "outputKnown": 1,
  "cachedKnown": 0,
  "unknownUsage": 1,
  "byAgent": [{"agent":"codex","attempts":1,"testAttempts":0,"renames":1,"kept":0,"inputTokens":50,"outputTokens":8,"cachedTokens":null,"inputKnown":1,"outputKnown":1,"cachedKnown":0,"unknownUsage":1}],
  "byModel": [{"provider":"codex","model":"gpt-6-luna","attempts":1,"testAttempts":0,"inputTokens":50,"outputTokens":8,"cachedTokens":null,"inputKnown":1,"outputKnown":1,"cachedKnown":0,"unknownUsage":1}]
}
```

`records.list {"limit":50}` 返回最近更新的事件及独立的连接测试调用列表 `testAttempts`，每个列表默认 50，最多 200。连接测试明细沿用事件调用的字段，按启动时间与日统计核对。用量各字段可独立为 `null`，`usageKnown` 为兼容标记，三项均已知时才为真。记录中没有对话和密钥。

```json
{
  "items": [{
    "id": "candidate-id",
    "agent": "codex",
    "sessionId": "native-session-1",
    "revision": "stable-turn-revision",
    "title": "🧩 构建｜修复路径",
    "baseTitle": "",
    "status": "applied",
    "syncStatus": "failed",
    "updatedAt": "2026-11-18T09:01:00+08:00",
    "attempts": [{
      "provider": "codex",
      "model": "gpt-6-luna",
      "source": "codex_subscription",
      "status": "completed",
      "startedAt": "2026-11-18T09:00:32+08:00",
      "requestedTier": "priority",
      "actualTier": "unknown",
      "inputTokens": 50,
      "outputTokens": 8,
      "cachedTokens": null,
      "usageKnown": false
    }]
  }],
  "testAttempts": []
}
```

| 字段 | 状态与含义 |
| --- | --- |
| `status` | `queued` 待命名、`running` 命名中、`candidate` 候选待领取、`claimed` 已交给宿主待读回、`applied` 原生名已读回、`kept` 保留原名、`stale` 已过期、`failed` 生成失败、`interrupted` 命名中断且未重发、`disabled` 插件停用、`native` 已有原生名的免模型同步记录 |
| `syncStatus` | 空字符串未调度、`unbound` 缺少绑定或原标题、`unchanged` Orca 标题已一致、`protected` Orca 标题受保护、`pending` 待同步、`writing` 同步中、`synced` 已同步并读回、`failed` 失败且可重试、`skipped` 安全跳过、`stale` 原生标题变化或绑定已过期 |
| `attempts[].status` | `running` 正在请求、`completed` 已收到决策、`failed` 服务失败、`interrupted` 中断未重发、`invalid_response` 响应无效、`rejected_tool` 尝试使用工具而被拒绝 |
| `attempts[].source` | `codex_subscription`、`openai_compatible`、`anthropic_compatible` 或 `gemini_compatible` |
| `requestedTier` | `priority` 或 `default` |
| `actualTier` | 由供应方证据确认的 `default`、`priority`、`flex`、`scale`，字段缺失或无法证实为 `unknown` |

### 同步失败与明确采用

`sync.retry {"id":"candidate-id"}` 仅将仍匹配当前原生标题和绑定的失败记录重新排队。`sync.adopt {"id":"candidate-id"}` 是用户明确选择“采用 Agent 标题”，只处理仍匹配当前原生标题和绑定的 `protected` 记录。两者均返回 `{"accepted":true}` 表示已排队，`false` 表示当前状态不可排队。两个动作均无模型调用。

宿主的 `syncer(ctx, rawOrcaSessionId, expectedTitle, newTitle)` 使用 `engine.SyncTargetFromContext(ctx)` 获取原生 Agent、会话 ID、可信原生 handle 和应读到的原生标题。在实际写入前重新核对原生会话与标题，再比较现场 Orca 原标题、写入和读回。原生或 Orca 证据不足时不写。重启和重试都复用同一检查。Orca 已等于目标且原生标题仍符合预期时可视作成功。

实时原生标题变化返回 `engine.ErrSyncStale`，现场安全跳过返回 `engine.ErrSyncSkipped`。这两类为终态。暂未拿到原生读回证据、写入失败或写后未获确认返回普通错误，记录为 `failed` 并允许重试。

## 状态存储与旧插件

模型发起前记录调用开始；数据库中的中断模型任务不重新提交，已排队但尚未开始的任务可恢复。仅在待命名期间暂存截取后的对话，开始工作与过期时清除它。宿主的插件管理负责对旧插件做可恢复的精确接管，本核心不导入旧插件的会话、用量或认证状态。现有标题可用上述免模型事件补同步。

命名规则复用已安装的 oil-codex-title 版本。Codex 临时调用参考 [oil-oil/oil-codex-title](https://github.com/oil-oil/oil-codex-title) 及 [yyxxd/agent-session-namer](https://github.com/yyxxd/agent-session-namer) 的原生调用方式。原作者 oil，Copyright (c) 2026，原 MIT 文本见 [LICENSE.naming](../internal/engine/LICENSE.naming)。
