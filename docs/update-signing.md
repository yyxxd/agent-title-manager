# 应用内更新签名配置

## 信任边界

应用只信任随 Release 发布的 `update-manifest.json`。清单由 Ed25519 私钥签名，覆盖 `version`、`arch`、`file`、`size`、`sha256`、`notes` 全部字段。Go 端在安装前完成：拉取最新稳定 Release → 跳过 draft/prerelease → 严格比较 `X.Y.Z`（禁止降级）→ 用内置公钥验签 → 核对资产大小 → 下载并校验 SHA-256 → 才交给更新 helper。任一环节失败都保留当前运行版本。

安装包名固定为 `AgentTitleManager-<版本>-windows-x64-setup.exe`，清单资产名为 `update-manifest.json`，架构标识为 `windows-x64`。

## 正式接线剩余项（当前未完成）

正式签名密钥尚未配置，应用内更新默认**禁用**（界面显示“正式签名密钥尚未配置，应用内更新暂不可用”）。要启用需由发布负责人完成：

1. 生成 Ed25519 密钥种子（32 字节，64 字符十六进制），例如用 `openssl rand -hex 32`。**仅保存在带外密钥管理器中，绝不写入 Git、CI 日志或本地配置文件。**
2. 在 GitHub 仓库添加 Secret `UPDATE_SIGNING_SEED`，值为上述种子。
3. 在 GitHub 仓库添加 Variable `UPDATE_PUBLIC_KEY`，值为由种子派生的 64 字符十六进制公钥。可用临时隔离环境执行 `ATM_SIGNING_SEED=<种子> go run ./internal/releasekey` 得到公钥。
4. 核对二者匹配：发布工作流第一步会自动派生公钥并与 Variable 比较，不匹配即失败。
5. 发布时 push `vX.Y.Z` tag，工作流注入公钥构建，用私钥签名清单，上传安装包与清单到 GitHub Release。
6. 验收新构建：`desktop.update.get` 的 `enabled` 为 `true`，界面不再显示禁用提示。

正式公钥刻意未写入源码：它由构建参数 `-UpdatePublicKey` 注入，与发布配置中的 Variable 同源。这样避免把测试信任根当作生产公钥提交。

## 临时测试密钥（仅隔离环境）

测试使用 `go test` 内 `ed25519.GenerateKey` 生成的临时密钥对，只存在于进程内存中；`httptest` 提供隔离 HTTP fixture，覆盖稳定版本比较、签名错误、哈希错误、网络失败与可重试。测试密钥对不落盘、不进入构建产物、不作为生产公钥。

隔离验收可另生成一对临时密钥，用 `build.ps1 -UpdatePublicKey <临时公钥>` 构建候选包，并让 `ATM_UPDATE_URL` 指向本地 fixture 服务。验收结束后丢弃该密钥对，不提交任何测试公钥或种子。

## 发布产物清单

普通发布前使用 `git tag -a vX.Y.Z --cleanup=verbatim -F release-notes.txt` 创建标签，保留中文说明里的 `#` 标题。签名清单和 GitHub Release 共用已校验的标签正文。

- `AgentTitleManager-<版本>-windows-x64-setup.exe`
- `update-manifest.json`（含签名字段）

安装包 SHA-256 与大小由发布工作流计算并写入清单，应用端二次校验。当前构建产物未做代码签名；清单签名是应用内更新的信任根，不替代 Windows 代码签名。

## 手动修复 v0.4.1 已发布说明

合并到 `main` 后，在 GitHub Actions 手动运行 **Repair v0.4.1 release notes**，分支选 `main`。也可以执行 `gh workflow run repair-v0.4.1-notes.yml --ref main`。

流程核对 v0.4.1 的远端标签对象、已发行安装包和原清单。原清单通过正式公钥验签后，只替换说明并重新签名。候选清单上传且读回摘要一致后，原资产改名为 `update-manifest.v0.4.1.original.json`，候选资产接替 `update-manifest.json`，随后同步并读回 Release 正文。

失败时可重跑同一工作流。候选上传失败时正式清单仍在。切换资产名的两个请求之间，正式清单可能短暂不可用。切换失败时，工作流尝试把备份恢复为正式清单。如果备份已就位，重跑可从备份继续。正式清单已替换时，重跑可继续同步 Release 正文。
