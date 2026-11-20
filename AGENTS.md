# Agent Title Manager 协作规范

## 版本发布与更新说明规范

应用内更新弹窗（`frontend/src/Update.tsx`）和 GitHub Release 页面会直接展示版本更新说明。发布任何新版本时必须遵守以下规则：

1. **必须使用简体中文**：面向最终用户说明本版本的新增功能、问题修复和体验优化，禁止直接粘贴英文 Git 提交记录、PR squash 摘要、`---------` 分隔线或 `Co-authored-by:` 元数据。
2. **排版保持紧凑**：每条更新说明占一行，使用 `- ` 开头，**条目之间不要插入空行**（应用内使用 `white-space: pre-wrap` 原样渲染，空行会导致列表间距过大）。
3. **通过 annotated tag 传入说明**：Git commit 仍遵循英文 Conventional Commits；发版打标签时必须使用 annotated tag（例如 `git tag -a v0.4.1 -F release-notes.txt`），把中文更新说明写入 tag 正文。`.github/workflows/release.yml` 会优先读取 annotated tag 正文，同步写入签名清单 `update-manifest.json` 与 GitHub Release。

示例格式：

```text
- 修复 Antigravity 在打包版 Node 环境下无法自动命名的问题
- 新增标题超长或格式不合规时的自动二次重试机制
- 优化连续对话的命名节流，减少过期记录与 Token 消耗
```
