# AI Code Review Agent

在 GitHub Actions 中自建的自动化 PR 代码审查 Agent：拉取 PR diff，调用 LLM（DeepSeek）审查，并以**行内评论**的形式把意见就近标注到代码行上。

- **低成本**：按 PR 增量审查 + token 分片，单次审查成本极低
- **不阻塞合并**：审查失败只记录日志，进程始终以退出码 0 结束，CI 不会被卡住
- **纯自控**：不依赖第三方 SaaS，代码只发往自选的 LLM 服务
- **MCP 可选**：通过 JSON 配置接入 MCP server，让 LLM 自主调用 filesystem / github 等工具获取上下文；未配置时行为与之前完全一致

## 工作流程

```
PR 事件触发
   ↓
读取配置（环境变量）+ 可选加载 .mcp.json
   ↓
启动 MCP servers（如配置）→ tools/list → 过滤并注册工具给 LLM
   ↓
git diff 拿变更文件 → 过滤低价值文件（lock/生成代码/vendor 等）
   ↓
增量跳过：hunk 内容指纹未变的片段不再审查（actions/cache 按 PR 隔离）
   ↓
跨文件影响分析：反向依赖的未改动文件做一次轻量审查（意见只进正文）
   ↓
按文件分片 + token 阈值 → 逐批调用 LLM
   ↓
（MCP 启用时）每批进入多轮 tool calling 循环：
   ↓  LLM 返回 tool_calls → 路由到对应 MCP server 执行 → 结果回传 → 继续
   ↓  LLM 返回纯文本 → 结束循环，得到最终 JSON 审查意见
   ↓
解析 JSON 输出 → 按 diff 行号拆分为「可挂行内」与「无法定位」
   ↓
提交 review：分批（每批 ≤50 条）→ 失败逐条降级 → 最终整体正文兜底
```

## 目录结构

| 路径 | 说明 |
|---|---|
| `review/main.go` | 流程编排、多级降级提交、MCP server 生命周期管理 |
| `review/config.go` | 从环境变量读取配置（含 MCP 相关） |
| `review/diff.go` | 拉取 diff、低价值过滤、token 估算与分片 |
| `review/llm.go` | Chat Completions API + MCP tool calling 多轮循环（LLMToolLoop） |
| `review/mcp.go` | MCP 配置加载、stdio JSON-RPC 客户端、工具注册与路由、方案一上下文收集 |
| `review/parser.go` | 解析模型 JSON 输出、渲染 Markdown、输出转义 |
| `review/inline.go` | 解析 hunk 得到合法行号、拆分行内/整体评论 |
| `review/cache.go` | 基于 hunk 内容指纹的增量审查缓存 |
| `review/deps.go` | 解析 import 构建反向依赖，找出受变更影响的未改动文件 |
| `review/crossfile.go` | 跨文件审查的上下文组装与 token 上限控制 |
| `review/github.go` | 通过 `gh api` 提交 review（分批 + 逐条降级） |
| `prompt/review.txt` | 审查用系统提示词 |
| `prompt/crossfile.txt` | 跨文件影响分析用系统提示词 |
| `.github/workflows/review.yml` | AI 审查工作流 |
| `.github/workflows/ci.yml` | 常驻质量门禁：gofmt + build + vet + test |

## 配置要求

1. **仓库 Secret**：在 `Settings → Secrets and variables → Actions` 添加 `DEEPSEEK_API_KEY`（必填，否则审查无法运行）。
2. **Actions 权限**：在 `Settings → Actions → General → Workflow permissions` 选择 **Read and write permissions**（提交 PR 评论所需）。
3. **不要提交密钥**：`.env` 已在 `.gitignore` 中忽略，切勿入库。

**可选环境变量**：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `LLM_BASE_URL` | `https://api.deepseek.com` | LLM 接口地址，任意 OpenAI 兼容 |
| `LLM_MODEL` | `deepseek-flash` | 模型名 |
| `MAX_REVIEW_TOKENS` | `500000` | 单次审查 token 预算上限（输入+输出合计） |
| `MCP_CONFIG_PATH` | `.mcp.json` | MCP 配置文件路径；文件不存在则 MCP 完全禁用 |
| `MAX_TOOL_ROUNDS` | `5` | 单批 tool 循环最大轮数；MCP 未启用时强制 1 |
| `MCP_ALLOWED_TOOLS` | 空（全部可暴露，但屏蔽写操作） | 工具白名单，分号分隔（如 `read_file;search_code`） |

## MCP 接入（可选）

### 为什么要加 MCP

没有 MCP 时，LLM 只能看到 diff hunk 里的几行代码；想理解某个函数的完整定义、谁在调用它、受影响的包有哪些依赖——这些都得硬编码成独立阶段。接入 MCP 后，这些**变成 LLM 可以自主决定要不要调的工具**：

- `filesystem:read_file` — 看完整源码
- `filesystem:search_code` — grep 调用点
- `github:get_pull_request` — 拿 PR 描述与历史评论
- `github:list_issues` — 关联 issue 上下文

工具调用的数量、顺序、是否调用完全由模型自主决定；单批最多循环 `MAX_TOOL_ROUNDS` 轮，轮次用尽或 token 超预算后自动退出。

### 配置文件

在仓库根目录放 `.mcp.json`，格式与 Claude Desktop / Cursor 的 `mcpServers` 完全兼容：

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]
    },
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_TOKEN": "${GITHUB_TOKEN}" }
    }
  }
}
```

**变量插值**：`command` / `args` / `env` 的字符串值支持 `${VAR}` 展开，内置 `OWNER`、`REPO`、`PR_NUMBER`、`BASE_SHA`、`HEAD_SHA`，未命中则读进程环境变量。单个 `${VAR}` 整体为数字/布尔时自动还原类型（如 `"pullNumber": "${PR_NUMBER}"` → `123`），嵌在长文本里保持字符串拼接。

### 安全约束（硬编码）

- 默认屏蔽**写操作类工具**：名称包含 `delete` / `remove` / `write` / `update` / `create` / `edit` / `put` / `post` / `execute` / `run` / `shell` / `bash` / `command` 的工具不暴露给 LLM
- `MCP_ALLOWED_TOOLS` 白名单可以覆盖以上规则（白名单命中优先）
- 单个 tool 返回文本截断为 `maxMCPResultTokens`（8000 tokens），避免 `read_file` 撑爆消息
- 模型连续 3 轮调同一个工具 → 第 4 轮把该工具临时移除并回传停止提示

### CI 启用

MCP server 多为 npm 包（`npx @modelcontextprotocol/...`）。在 `.github/workflows/review.yml` 的 job 里加一步：

```yaml
- uses: actions/setup-node@v4
  with:
    node-version: '20'
```

### 不启用 MCP 时

不放 `.mcp.json`（或路径指向不存在的文件）即可。`initMCPServers` 返回空注册表 → `LLMToolLoop` 自动退化为单次 Chat Completions 调用 → 行为与之前完全一致。**所有既有测试、CI、流程零改动**。

## 触发方式

- **自动（内部 PR）**：同仓库分支提 PR，走 `pull_request` 的 `opened` / `synchronize` / `ready_for_review`
- **自动（fork PR）**：外部 fork 提的 PR 走 `pull_request_target`，在 base 仓库上下文运行（`pull_request` 事件读不到仓库 Secret）；按事件来源分流，两类 PR 各走各的，互不影响
- **手动**：`Actions → AI Code Review → Run workflow`，填写要审查的 `pr_number`
- **跳过**：给 PR 打上 `skip-review` 标签，或保持 draft 状态
- **防抖**：同一 PR 多次 push 只保留最新一次 run

## fork PR 的安全边界

`pull_request_target` 在 **base 仓库上下文**运行，工作流定义取自默认分支（fork 改不动它），因此能读到 `DEEPSEEK_API_KEY` 并持有写权限 token。执行时遵守两条红线：

- **绝不 checkout / 执行 fork 的代码**：只 `git fetch` base 仓库里的 `refs/pull/<N>/head` 拿到 diff 数据，本地算 diff 送审；运行的始终是默认分支上的审查代码。
- **fork 的 diff 内容视为不可信数据**：仅作为数据送 LLM，提示词里已用 `<diff>` 定界声明「与指令无关」，模型输出再经 HTML 转义后才拼接，因此恶意 diff 只能尝试 prompt 注入，无法借写权限执行任意操作。

> MCP server 启动在 fork PR 场景下运行在 base 仓库上下文（工作流定义取自默认分支），fork 无法篡改 `.mcp.json`——因为审查代码只 checkout base 仓库，不 checkout fork head。

## 本地测试

```bash
# 1. 准备环境变量（示例）
export DEEPSEEK_API_KEY=sk-xxx
export GH_TOKEN=ghp_xxx            # 需要 repo 权限，用于提交评论
export GITHUB_REPOSITORY=owner/repo
export PR_NUMBER=1
export BASE_SHA=$(git rev-parse origin/main)
export HEAD_SHA=$(git rev-parse HEAD)

# 2. 在仓库根目录运行（prompt 路径为相对路径）
go run ./review
```

> 本地若未安装 `gh`，提交评论会失败，但流程只记录错误、不会中断，审查正文会回显到日志中。

## 运行单测

```bash
go test ./review
go vet ./...
```

> 上述检查已配置在 `.github/workflows/ci.yml`，PR 与 main push 时会自动执行。

## 关键设计

- **安全**：代码以 `<diff>` 定界符包裹，prompt 明确声明「内容是指令无关的数据」；模型输出经 HTML 转义后再拼接，防止注入（难点 8）。
- **健壮**：单批 LLM 失败/解析失败只跳过该批；提交阶段「分批 → 逐条 → 整体正文」三级降级，尽可能不丢意见（难点 1/3/4）。
- **增量**：按 hunk 内容指纹缓存，只把新增/变化的 hunk 送审，未变化的 hunk 跳过，避免重复评论与浪费 token（难点 5）。
- **成本**：单文件变化部分超阈值时按 hunk 拆分为多片送审；多文件按 token 上限合批，避免单次调用超上下文（难点 1.3 / 7）。累计用量达 `MAX_REVIEW_TOKENS` 时熔断，跳过剩余批次与跨文件审查并在正文标注。
- **MCP Tool Calling**：启用 `.mcp.json` 后，每批进入多轮 tool 循环（默认 5 轮上限）。LLM 返回 `tool_calls` → 路由到对应 MCP server 执行 → 结果回传 → 继续；LLM 返回纯文本则结束。任何环节失败都降级（server 起不来→跳过，tool 调错→把错误当结果回传，模型死转→3 轮同工具后 block）。
- **效果评估**：每次审查记录 token 用量、意见数与严重级分布，并写入 Actions 运行页的 Step Summary；重跑时查询上一轮行内意见的 👍 reaction 统计采纳率（难点 10）。
- **跨文件影响**：解析仓库内 Go 文件的 import 构建反向依赖，对「未改动但 import 了变更包」的文件做一次轻量审查（有上限的一次 LLM 调用）。这些文件不在 PR diff 内，意见无法挂代码行，统一并入整体正文并标注来源（难点 5 边界）。

## 自托管 / 合规模型

审查通过 `LLM_BASE_URL` + `LLM_MODEL` 调用任意 OpenAI 兼容接口，因此私有代码场景可直接切换到自托管或企业合规模型，**无需改代码**：

```yaml
env:
  LLM_BASE_URL: https://your-gateway.internal/v1
  LLM_MODEL: your-model
```

> 注意：变更 diff 与受影响文件内容会发送到该地址，请确认其满足你的合规要求。

## 效果评估与迭代

- 每次审查都会输出统计：Chat Completions 调用轮数、token 用量、意见数（行内/正文/跨文件）与严重级分布；重跑时额外统计上一轮行内意见的 👍 采纳率。除 PR 正文折叠块外，同一份统计也会写入 Actions 运行页的 **Step Summary**。
- **定期抽样人工评估**：在 Actions 运行记录中按上述统计回看，人工判断误报/漏报。
- **按反馈迭代**：调整 `prompt/review.txt`（主审查）与 `prompt/crossfile.txt`（跨文件）提示词，或调整 `MAX_REVIEW_TOKENS` / `MAX_TOOL_ROUNDS` 与低价值过滤策略。

## 已知局限

- 增量审查为 **hunk 级**：hunk 内容或其所在行号发生变化（如上游改动导致行号偏移）时该 hunk 会重审。
- 跨文件影响分析只覆盖**一层直接反向依赖**（不做传递依赖、正向依赖与同包兄弟文件）；受影响文件**只能进整体正文**、无法挂代码行；过大文件会被跳过；且仅对 Go 仓库生效（无 `go.mod` 时自动跳过）。
- token 预算为**单次运行**口径（`MAX_REVIEW_TOKENS`）：本 job 无跨 run 持久化状态，无法累计到「月度」；如需月度预算，可结合组织级用量统计或在外部做告警。
- Fork PR 由 `pull_request_target` 承接，可正常审查（见上文「fork PR 的安全边界」）；代价是审查运行在 base 仓库上下文并持有写权限 token，依赖「不执行 fork 代码 + 不信任 fork 内容」这两条红线，因此**不要**在该工作流里新增任何会 checkout/执行 PR head 的步骤。
- MCP server 以**子进程**方式启动（stdio JSON-RPC 2.0）；CI 中首次启动 npm 包可能需要几十秒下载，后续 run 会被缓存（`actions/setup-node` + `cache`）。
