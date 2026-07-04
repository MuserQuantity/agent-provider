# agent-provider

Expose Claude Code, Devin CLI, Grok, Codex and other AI agent CLIs as OpenAI-compatible models.

把本机各家 agent CLI 的"单行命令执行方式"包装成 OpenAI 兼容的 HTTP 接口。任何支持自定义 OpenAI Base URL 的客户端（如各种聊天客户端、SDK、IDE 插件）都能直接调用本机的 agent CLI。

## 支持的后端

| model 名 | 实际执行的命令 | prompt 传递方式 |
|---|---|---|
| `devin` | `devin -p --prompt-file <tmp>` | 临时文件 |
| `grok` | `grok --prompt-file <tmp>` | 临时文件 |
| `claude` | `claude -p` | stdin |
| `codex` | `codex exec -` | stdin |
| `opencode` | `opencode run <prompt>` | 命令行参数 |
| `gemini` | `gemini -p <prompt>` | 命令行参数 |

- `GET /v1/models` 只会列出本机 PATH 里实际存在的后端。
- `model` 支持用 `:` 透传内层模型，例如 `devin:swe-1.6-fast` → `devin --model swe-1.6-fast`，`grok:grok-4-fast` → `grok -m grok-4-fast`。
- devin 可用模型（`devin --model <无效值>` 报错时会列出）：`adaptive`、`swe-1.6-fast`、`swe-1.6`、`swe-1.5`、`claude-*`、`gpt-*`、`gemini-*` 等；轻量任务（如翻译）推荐 `devin:swe-1.6-fast`。

## 构建与启动

```powershell
go build -o agent-provider.exe .
.\agent-provider.exe
```

启动参数（也可用环境变量或 `.env` 文件配置，优先级：命令行参数 > 环境变量 > `.env` > 默认值，参考 `.env.example`）：

| 参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-addr` | `AGENT_PROVIDER_ADDR` | `127.0.0.1:8080` | 监听地址 |
| `-proxy` | `AGENT_PROVIDER_PROXY` | `http://127.0.0.1:7890` | 注入给 CLI 子进程的 `all_proxy/http_proxy/https_proxy`；传空字符串 `-proxy ""` 关闭 |
| `-workdir` | `AGENT_PROVIDER_WORKDIR` | `.` | CLI 子进程的工作目录 |
| `-timeout` | `AGENT_PROVIDER_TIMEOUT` | `10m` | 单次 CLI 执行的最长时间 |
| `-api-key` | `AGENT_PROVIDER_API_KEY` | 空 | 设置后要求请求带 `Authorization: Bearer <key>` |

## API

OpenAI 兼容的三个接口：

- `POST /v1/chat/completions`（支持 `"stream": true` SSE 流式）
- `POST /v1/completions`（legacy 文本补全）
- `GET /v1/models`

```bash
# 非流式
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"devin","messages":[{"role":"user","content":"你好"}]}'

# 流式
curl -N http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"devin","messages":[{"role":"user","content":"你好"}],"stream":true}'
```

配合 OpenAI SDK：

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:8080/v1", api_key="none")
resp = client.chat.completions.create(
    model="devin",
    messages=[{"role": "user", "content": "你好"}],
)
print(resp.choices[0].message.content)
```

## 浏览器插件（如 OpenAI Translator）

服务已开启 CORS（含 OPTIONS preflight），浏览器端客户端可直接调用。在插件设置里：

- API URL：`http://127.0.0.1:8080`（路径按插件要求，通常自动拼 `/v1/chat/completions`）
- API Key：填 `.env` 里配置的 `AGENT_PROVIDER_API_KEY`；没配置鉴权就随便填
- 模型名：填 `devin`（或 `grok` 等）

插件自带的系统提示词没有问题：`system + user` 两条消息会被拼成一个 prompt 交给 CLI。注意由于开启了 CORS，任意网页的 JS 理论上都能访问本服务，建议配置 `AGENT_PROVIDER_API_KEY`。

## 部署到 Ubuntu 服务器

### 1. 准备二进制

方式 A（推荐）：本机交叉编译后上传，服务器无需装 Go：

```powershell
$env:GOOS='linux'; $env:GOARCH='amd64'; go build -o agent-provider-linux-amd64 .
scp .\agent-provider-linux-amd64 ubuntu@服务器:/opt/agent-provider/agent-provider
```

方式 B：服务器上从源码构建（需要 Go 1.26+，apt 里的版本太旧，用 snap）：

```bash
sudo snap install go --classic
git clone https://github.com/MuserQuantity/agent-provider.git
cd agent-provider && go build -o agent-provider .
```

### 2. 服务器上安装并登录 devin CLI

```bash
curl -fsSL https://cli.devin.ai/install.sh | bash
# SSH 环境没有浏览器，用手动 token 登录：
devin auth login --force-manual-token-flow
devin auth status
# 先确认 CLI 本身能跑通，再谈服务：
devin -p "Reply with exactly one word: pong"
```

### 3. 配置 `/opt/agent-provider/.env`

```
AGENT_PROVIDER_ADDR=0.0.0.0:8080
AGENT_PROVIDER_API_KEY=<强随机串>
AGENT_PROVIDER_PROXY=none
```

- 服务器不需要本机那个 7890 代理时**必须**写 `AGENT_PROVIDER_PROXY=none`（或启动时传 `-proxy ""`），否则默认会给 CLI 注入 `http://127.0.0.1:7890`，网络会全挂。
- 安全提醒：持有 key 的人相当于能在你服务器上驱动 agent（读写 `-workdir`、执行命令）。key 要设强；能只监听 `127.0.0.1` 配合 SSH 隧道/nginx 就不要直接开 `0.0.0.0`。

### 4. systemd 常驻

`/etc/systemd/system/agent-provider.service`：

```ini
[Unit]
Description=agent-provider OpenAI-compatible gateway
After=network-online.target
Wants=network-online.target

[Service]
User=ubuntu
WorkingDirectory=/opt/agent-provider
ExecStart=/opt/agent-provider/agent-provider
Environment="PATH=/home/ubuntu/.local/bin:/usr/local/bin:/usr/bin:/bin"
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

注意：`User` 必须是执行过 `devin auth login` 的那个用户（凭据存在其 home 下），`PATH` 必须包含 devin 的安装目录（默认 `~/.local/bin`）。

```bash
sudo mkdir -p /opt/agent-provider && sudo chown ubuntu:ubuntu /opt/agent-provider
chmod +x /opt/agent-provider/agent-provider
sudo systemctl daemon-reload
sudo systemctl enable --now agent-provider
journalctl -u agent-provider -f   # 看日志
```

### 5. 验证

```bash
curl http://127.0.0.1:8080/v1/models -H "Authorization: Bearer <key>"

curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" -H "Authorization: Bearer <key>" \
  -d '{"model":"devin:swe-1.6-fast","messages":[{"role":"user","content":"你好"}]}'
```

## 实现说明

- 多轮 `messages` 会被拍平：单条 user 消息原样透传；`system + user` 两条直接拼接；更长的对话转成带 `[role]` 标签的 transcript 交给 CLI 续写。
- prompt 通过临时文件或 stdin 传递，避免 Windows 命令行长度限制（约 32K 字符）。
- SSE 流式按 UTF-8 字符边界切分 chunk，中文不会在分块处出现乱码；但 CLI 本身不输出 token 级增量（如 `devin -p` 一次性打印完整回复），所以流式实际是"CLI 输出多少就转发多少"。
- `usage` 中的 token 数为估算值（约 4 字节/token），CLI 不回报真实用量。
- 请求体兼容 UTF-8 BOM（Windows 客户端常见）。
- 新增后端只需在 `backends.go` 的 `backends` 表里加一项。

## License

MIT
