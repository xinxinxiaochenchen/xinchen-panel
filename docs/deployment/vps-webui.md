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

创建节点后，用管理员权限生成一次性入网令牌；令牌只写入服务器私有文件，不打印到标准输出。Agent 使用 `compose.agent-tls.yaml` 连接控制面，生产多跳线路再叠加 `compose.relay-secrets.yaml`。Agent 的证书、CA 私钥、代理服务端证书和线路密钥都应放在发布目录之外。

```sh
docker compose -f compose.yaml -f compose.proxy-secrets.yaml ps
docker compose -f compose.yaml -f compose.proxy-secrets.yaml logs --tail=100 api
```

升级前先备份 PostgreSQL，再替换发布目录并执行迁移；Compose 不会自动执行 down migration。
