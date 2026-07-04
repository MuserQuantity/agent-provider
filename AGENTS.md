# AGENTS.md

面向在本仓库工作的开发者与 coding agent 的实战经验记录。

## 项目速览

- Go 1.26+，纯标准库，零第三方依赖。
- 文件职责：`main.go` 启动/配置/中间件；`backends.go` CLI 后端表与 prompt 传递；`handler.go` OpenAI 接口与 SSE 流式；`types.go` wire 类型。
- 构建与检查：`go vet ./...` && `go build -o agent-provider.exe .`
- Linux 产物：`$env:GOOS='linux'; $env:GOARCH='amd64'; go build -o agent-provider-linux-amd64 .`（产物已 gitignore）

## 本地验证流程

1. 起服务用测试端口：`.\agent-provider.exe -addr 127.0.0.1:18080`
2. `curl.exe http://127.0.0.1:18080/v1/models`
3. chat 请求的 JSON body 先写临时文件再 `curl.exe -d "@文件"`（绕开 PowerShell 引号问题）
4. devin 后端冒烟 prompt 用 `"Reply with exactly one word: pong"`，单次 6~12s 属正常

## Windows 开发坑（都踩过）

- PowerShell 5.1 的 `Set-Content -Encoding utf8` 会写 UTF-8 BOM：测试 body、git commit 消息文件一律用 `-Encoding ascii`。服务端 `decodeJSON` 已兼容带 BOM 的请求体。
- `git commit -m "$(cat <<'EOF'...)"` 是 bash 语法，PowerShell 下必须改用 `git commit -F <消息文件>`。
- 杀掉跑 `go run` 的 shell 不会杀掉子进程，端口会被占住。清理：
  `Get-NetTCPConnection -LocalPort <port> -State Listen | % { Stop-Process -Id $_.OwningProcess -Force }`
  所以优先 `go build` 出 exe 直接跑，别用 `go run`。
- 本机网络：devin/grok 需要代理，服务默认给 CLI 子进程注入 `http://127.0.0.1:7890`；git push 也要在 shell 里设 `$env:https_proxy`。

## devin CLI 行为（实测结论，2026-07）

- 非交互：`devin -p --prompt-file <f>`，`--model` 可透传；传无效模型名时报错会列出全部可用模型 ID（`swe-1.6-fast`、`swe-1.6`、`adaptive`、`claude-*`…）。
- 延迟：每次 `-p` 有约 5~6s 的固定会话开销（与模型无关）；`swe-1.6(-fast)` 端到端 6~9s，默认模型约 12s。
- session 堆积：每次 `-p` 都会按 cwd 持久化一个 session（`%APPDATA%\devin\cli\sessions.db`，Linux `~/.local/share/devin/cli/sessions.db`），**没有关闭开关**；`/rm-session` 只在交互 REPL 有效，放进 `-p` 会被当成普通 prompt 反而再建一个 session。缓解：服务 workdir 指向专用 scratch 目录。
- 无头登录（SSH 服务器）：`devin auth login --force-manual-token-flow`。
- 未来延迟优化方向：`devin acp`（stdio 上的 JSON-RPC 常驻进程）。已验证 `initialize` 握手仅 26ms；但探测时 `session/new` 后卡死过——实现真正的 ACP 客户端时务必：异步读干 stderr（管道写满会死锁）、处理 agent→client 的请求（如 `session/request_permission`）、留意 MCP 服务器连接可能阻塞会话创建。

## 部署注意

- 完整步骤见 README「部署到 Ubuntu 服务器」。
- systemd 两大坑：`User=` 必须是执行过 `devin auth login` 的用户；`Environment=PATH` 必须包含 `~/.local/bin`（devin 安装位置）。
- `.env` 只在进程启动时读取一次，改完必须 `sudo systemctl restart agent-provider`（invalid API key 十有八九是这个原因）。
- 服务器上必须 `AGENT_PROVIDER_PROXY=none`，否则 CLI 会被注入不存在的 7890 代理导致全部超时。
