# AI Code Review Agent

在 GitHub Actions 中自建的自动化 PR 代码审查 Agent：拉取 PR diff，调用 LLM（DeepSeek）审查，并以**行内评论**的形式把意见就近标注到代码行上。

- **低成本**：按 PR 增量审查 + token 分片，单次审查成本极低
- **不阻塞合并**：审查失败只记录日志，进程始终以退出码 0 结束，CI 不会被卡住
- **纯自控**：不依赖第三方 SaaS，代码只发往自选的 LLM 服务

## 工作流程

```
PR 事件触发
   ↓
读取配置（环境变量）
   ↓
git diff 拿变更文件 → 过滤低价值文件（lock/生成代码/vendor 等）
   ↓
增量跳过：blob hash 未变的文件不再审查（actions/cache 按 PR 隔离）
   ↓
按文件分片 + token 阈值 → 逐批调用 LLM
   ↓
解析 JSON 输出 → 按 diff 行号拆分为「可挂行内」与「无法定位」
   ↓
提交 review：分批（每批 ≤50 条）→ 失败逐条降级 → 最终整体正文兜底
```

## 目录结构

| 路径 | 说明 |
|---|---|
| `review/main.go` | 流程编排、多级降级提交 |
| `review/config.go` | 从环境变量读取配置 |
| `review/diff.go` | 拉取 diff、低价值过滤、token 估算与分片 |
| `review/llm.go` | 调用 DeepSeek Responses API |
| `review/parser.go` | 解析模型 JSON 输出、渲染 Markdown、输出转义 |
| `review/inline.go` | 解析 hunk 得到合法行号、拆分行内/整体评论 |
| `review/cache.go` | 基于 blob hash 的增量审查缓存 |
| `review/github.go` | 通过 `gh api` 提交 review（分批 + 逐条降级） |
| `prompt/review.txt` | 审查用系统提示词 |
| `.github/workflows/review.yml` | GitHub Actions 工作流 |

## 配置要求

1. **仓库 Secret**：在 `Settings → Secrets and variables → Actions` 添加 `DEEPSEEK_API_KEY`（必填，否则审查无法运行）。
2. **Actions 权限**：在 `Settings → Actions → General → Workflow permissions` 选择 **Read and write permissions**（提交 PR 评论所需）。
3. **不要提交密钥**：`.env` 已在 `.gitignore` 中忽略，切勿入库。

可选环境变量：`LLM_BASE_URL`（默认 `https://api.deepseek.com`）、`LLM_MODEL`（默认 `deepseek-flash`）。

## 触发方式

- **自动**：`pull_request` 的 `opened` / `synchronize` / `ready_for_review`
- **手动**：`Actions → AI Code Review → Run workflow`，填写要审查的 `pr_number`
- **跳过**：给 PR 打上 `skip-review` 标签，或保持 draft 状态
- **防抖**：同一 PR 多次 push 只保留最新一次 run

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

## 关键设计

- **安全**：代码以 `<diff>` 定界符包裹，prompt 明确声明「内容是指令无关的数据」；模型输出经 HTML 转义后再拼接，防止注入（难点 8）。
- **健壮**：单批 LLM 失败/解析失败只跳过该批；提交阶段「分批 → 逐条 → 整体正文」三级降级，尽可能不丢意见（难点 1/3/4）。
- **增量**：按文件 blob hash 缓存，内容未变的文件跳过，避免重复评论与浪费 token（难点 5）。
- **成本**：单文件 diff 超阈值跳过并标记；多文件按 token 上限合批，避免单次调用超上下文（难点 7）。
- **效果评估**：每次审查记录 token 用量、意见数与严重级分布；重跑时查询上一轮行内意见的 👍 reaction 统计采纳率（难点 10）。

## 已知局限

- 增量审查为**文件级**：文件一旦变化即整文件重审，不做 hunk 级增量。
- 不做跨文件影响分析（依赖文件变更导致的问题可能漏报）。
- Fork PR 无法读取仓库 Secret，因此不会触发审查。