# VPS WebUI 部署

本项目是浏览器访问的 Web 控制台，不包含原生 App。控制面、前端静态资源和 PostgreSQL 通过 Docker Compose 运行；节点数据面由独立 Agent 运行。本说明适用于 Linux amd64 VPS。

## 1. 构建发布包

在构建机的项目根目录准备 Go 1.27.1、Node.js 22，然后执行：

```sh
./scripts/build-linux-amd64.sh
```

脚本会生成 `bin/` 下的 Linux amd64 控制面、迁移、管理员初始化、节点初始化、Agent 和 Agent 入网令牌程序，并构建 `apps/web/dist`。把完整项目目录连同 `bin/`、`apps/web/dist`、`migrations/` 和 `deployments/compose/` 传到 VPS 的一个版本目录；不要把本机 `.env`、私钥或数据库文件放进发布包。VPS 需要 Docker Engine 与 Compose 插件。

## 2. 启动控制面和 WebUI

进入 Compose 目录，创建权限为 `0600` 的 `.env`。数据库密码使用足够长的十六进制随机串，避免连接 URL 中的保留字符：

```dotenv
POSTGRES_PASSWORD=替换为至少32位十六进制随机串
CONTROL_BIND_IP=127.0.0.1
CONTROL_BROWSER_AUTH_ENABLED=false
```

启动数据库、迁移和控制面：

```sh
docker compose -f compose.yaml build
docker compose -f compose.yaml up -d db migrate api
curl http://127.0.0.1:18080/api/v1/health/ready
```

以上配置只绑定服务器回环地址，由 Nginx/Caddy 负责 HTTPS 反代。如需从公网纯 IP 临时查看只读页面，可将 `CONTROL_BIND_IP` 设为 `0.0.0.0` 并保持 `CONTROL_BROWSER_AUTH_ENABLED=false`；此模式不提供登录、写操作或订阅 Token。生产管理入口须恢复为回环绑定并使用 HTTPS。

## 3. 启用浏览器登录

先在服务器上创建 32 字节的代理凭据加密密钥，文件权限必须为 `0600`：

```sh
install -d -o 65532 -g 65532 -m 0700 /opt/network-control-plane/secrets/proxy
python3 - <<'PY' > /opt/network-control-plane/secrets/proxy/proxy.key
import base64, os
print(base64.urlsafe_b64encode(os.urandom(32)).decode().rstrip('='))
PY
chmod 0600 /opt/network-control-plane/secrets/proxy/proxy.key
chown 65532:65532 /opt/network-control-plane/secrets/proxy/proxy.key
```

将 `.env` 改为：

```dotenv
CONTROL_BROWSER_AUTH_ENABLED=true
CONTROL_PROXY_CREDENTIAL_SECRET_DIR=/opt/network-control-plane/secrets/proxy
```

先通过标准输入创建管理员：

```sh
read -r -s -p '管理员初始密码: ' NCP_ADMIN_PASSWORD
printf '\n'
printf '%s\n' "$NCP_ADMIN_PASSWORD" | docker compose -f compose.yaml run --rm -T \
  -e CONTROL_ADMIN_EMAIL='admin@example.com' api \
  /usr/local/bin/admin-bootstrap
unset NCP_ADMIN_PASSWORD
```

该命令从标准输入读取管理员密码，运行时输入至少 12 字节的密码。再加载密钥覆盖文件并重建 API：

```sh
docker compose -f compose.yaml -f compose.proxy-secrets.yaml up -d --force-recreate api
```

## 4. HTTPS 反代

Nginx 只需要把 HTTPS 管理域名反代到 `127.0.0.1:18080`，并转发 WebSocket Upgrade 头；不要把 PostgreSQL 端口映射到公网。启用浏览器登录前，确保 `CONTROL_BIND_IP` 已恢复为 `127.0.0.1`，并为反代配置真实客户端 IP 限流。

## 5. 节点 Agent

创建节点后，用管理员权限生成一次性入网令牌；令牌只写入控制面服务器私有文件，不打印到标准输出。Agent 使用 `compose.agent-local.yaml` 在节点 VPS 上运行，控制面只运行 `compose.agent-tls.yaml`。生产多跳线路再叠加 `compose.relay-secrets.yaml`。Agent 的证书、CA 私钥、代理服务端证书和线路密钥都应放在发布目录之外。

### 5.1 控制面准备 Agent TLS

控制面需要专用 CA、服务端证书和私钥，并只通过 HTTPS/mTLS 入口暴露 Agent 路径。服务端证书 SAN 必须覆盖节点实际使用的域名或 IP；浏览器管理入口仍由 Nginx/Caddy 反代到 `127.0.0.1:18080`。控制面示例：

```sh
CONTROL_AGENT_TLS_DIR=/opt/network-control-plane/secrets/agent-tls \
  docker compose -f compose.yaml -f compose.agent-tls.yaml config --quiet
CONTROL_AGENT_TLS_DIR=/opt/network-control-plane/secrets/agent-tls \
  docker compose -f compose.yaml -f compose.agent-tls.yaml up -d --build api
```

将 CA 公钥 `agent-ca.crt` 安全复制到节点；CA 私钥和 `server.key` 永远留在控制面。

### 5.2 独立节点 VPS 入网

在节点 VPS 上只上传项目发布包和 `agent-ca.crt`，准备目录并设置 UID 65532 可写：

```sh
install -d -m 0700 /opt/network-control-plane/secrets/agent-credentials
install -d -m 0700 /opt/network-control-plane/state/agent
chown 65532:65532 /opt/network-control-plane/secrets/agent-credentials /opt/network-control-plane/state/agent
```

在控制面服务器为已创建的节点签发一次性令牌。先准备仅管理员可读写的临时目录；`agent-token` 只把令牌 JSON 写入该目录，不会打印令牌：

```sh
install -d -m 0700 /run/private
chown 65532:65532 /run/private
docker compose -f compose.yaml run --rm -T \
  -e CONTROL_ADMIN_EMAIL='admin@example.com' \
  -e CONTROL_AGENT_TOKEN_OUTPUT_FILE=/run/private/agent-token.json \
  -v /run/private:/run/private:rw \
  api /usr/local/bin/agent-token <node-uuid>
chmod 0600 /run/private/agent-token.json
```

这条命令必须在控制面发布目录的 Compose 目录执行；`CONTROL_DATABASE_URL` 由 Compose 根据 `.env` 中的数据库密码注入。令牌文件只通过受控文件传输送到节点 VPS，成功入网后立即删除控制面和节点上的临时副本。

管理员在控制面为节点生成一次性令牌后，把令牌 JSON 以 `0600` 临时文件形式放到节点，使用标准输入完成入网：

```sh
export CONTROL_AGENT_STREAM_URL='wss://control.example.com:18443/api/v1/agent/stream'
export CONTROL_AGENT_ENROLL_URL='https://control.example.com:18443/api/v1/agent/enroll'
export CONTROL_AGENT_CREDENTIAL_DIR=/opt/network-control-plane/secrets/agent-credentials
export CONTROL_AGENT_STATE_DIR=/opt/network-control-plane/state/agent
export CONTROL_AGENT_NODE_ID='<node-uuid>'
export CONTROL_AGENT_VERSION='0.1.0'
python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' /run/private/agent-token.json | \
  docker compose -f compose.agent-local.yaml run --rm -T agent enroll
rm -f /run/private/agent-token.json
docker compose -f compose.agent-local.yaml up -d --build agent
```

`compose.agent-local.yaml` 使用 host network，因此节点的代理、转发和中继端口直接绑定节点 VPS。需要代理能力时，把 `proxy.crt`/`proxy.key` 放入凭据目录并设置对应环境变量；需要中继能力时设置 `CONTROL_AGENT_RELAY_HOST`、`CONTROL_AGENT_RELAY_PORT`、`CONTROL_AGENT_RELAY_CERT_FILE=/run/agent/relay.crt` 和 `CONTROL_AGENT_RELAY_KEY_FILE=/run/agent/relay.key`。节点必须能验证控制面 CA。18443 是专用 TLS/mTLS 入口，优先直连并限制来源 IP；若经过反代，必须保持端到端 TLS 或正确传递客户端证书，并支持 WebSocket Upgrade。

### 5.3 同机节点

如果 Agent 与控制面在同一台 VPS，使用 `wss://127.0.0.1:18443/api/v1/agent/stream` 和 `https://127.0.0.1:18443/api/v1/agent/enroll`，并确保服务端证书包含 `127.0.0.1` SAN。独立节点示例中的公网控制面地址不能直接套用到同机回环场景。

```sh
docker compose -f compose.yaml -f compose.proxy-secrets.yaml ps
docker compose -f compose.yaml -f compose.proxy-secrets.yaml logs --tail=100 api
```

升级前先备份 PostgreSQL，再替换发布目录并执行迁移；Compose 不会自动执行 down migration。
