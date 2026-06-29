# CPA - CLI Proxy API (Docker 部署版)

CLIProxyAPI 的 Docker 一键部署版本。将 CLI 模型（Gemini、Claude Code、OpenAI Codex、Grok）包装为 OpenAI 兼容 API。

## 快速启动

```bash
# 构建并启动
docker compose up -d

# 查看日志
docker compose logs -f

# 停止
docker compose down
```

## 访问地址

| 项目 | 地址 |
|------|------|
| API 服务 | `http://<服务器IP>:8317/` |
| 管理面板 | `http://<服务器IP>:8317/management.html` |

## 认证信息

| 项目 | 默认值 |
|------|--------|
| API Key | `sk-cpa-key-001` |
| 管理密钥 | `admin123` |

管理 API 通过 `Authorization: Bearer admin123` 请求头认证。

## 管理 API

```bash
# 查看配置
curl http://localhost:8317/v0/management/config \
  -H 'Authorization: Bearer admin123'

# 查看 YAML 配置
curl http://localhost:8317/v0/management/config.yaml \
  -H 'Authorization: Bearer admin123'
```

## 添加 AI 提供商

### Gemini API Key

编辑 `config.yaml`，添加：

```yaml
gemini-api-key:
  - api-key: "AIzaSy..."
```

或通过管理面板添加。

### OAuth 登录（Claude Code / Codex / Grok）

```bash
# Claude Code
docker exec -it cpa /CLIProxyAPI/CLIProxyAPI -claude-login

# Codex
docker exec -it cpa /CLIProxyAPI/CLIProxyAPI -codex-login
```

## 目录结构

```
cpa/
├── config.yaml           # CPA 配置文件
├── Dockerfile            # Docker 构建文件
├── docker-compose.yml    # Docker Compose 配置（一键部署）
├── auths/                # OAuth 认证文件目录
└── logs/                 # 日志目录
```
