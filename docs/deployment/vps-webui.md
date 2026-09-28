# VPS WebUI 部署

本项目是浏览器访问的 Web 控制台，不包含原生 App。控制面、前端静态资源和 PostgreSQL 通过 Docker Compose 运行；节点数据面由独立 Agent 运行。本说明适用于 Linux amd64 VPS。

## 从 GitHub 一键启动只读 WebUI

VPS 先安装 curl、Git、Docker Engine 和 Compose 插件（[Docker 官方安装说明](https://docs.docker.com/engine/install/)）。以 root 运行以下一条命令，下载安装并启动纯 IP 只读预览：

```sh
curl -fsSL https://raw.githubusercontent.com/xinxinxiaochenchen/xinchen-panel/main/scripts/install-vps.sh -o xinchen-panel-install.sh && sh xinchen-panel-install.sh --public-preview
```

完成后访问 `http://服务器IP:18080/`。如公网无法访问，检查服务器与云平台防火墙的 TCP 18080 规则。脚本只检查 Docker 和 Git，不自动安装或升级系统依赖。首次编译需要访问 GitHub、npm、Go 模块和容器镜像仓库，建议为源码构建准备至少 2 GiB 可用内存；低内存 VPS 可采用下文预编译发布包流程。

安装目录默认为 `/opt/xinchen-panel`；用 `XINCHEN_PANEL_DIR=/absolute/path sh xinchen-panel-install.sh --public-preview` 可选择其他可写目录。入口脚本首次克隆 `main`，重复运行时只在同仓库、无本地改动的 `main` 分支上执行 `git pull --ff-only`，遇到不同仓库、其他分支、冲突或本地改动会停止，不覆盖文件。下载命令使用 `&&`，下载失败不会执行不完整脚本。已克隆源码的用户仍可运行 `sh scripts/deploy-vps.sh [--public-preview]`。

脚本直接在 Docker 中编译 Go 控制面与 React WebUI，不要求 VPS 预装 Go 或 Node.js；首次运行会生成 `deployments/compose/.env`（权限 `0600`、随机数据库密码），执行 migration，并等待 API 健康检查。省略 `--public-preview` 时只在 `127.0.0.1:18080` 提供 HTTP，由你自己的 Nginx/Caddy HTTPS 反代。纯 IP 模式绑定 `0.0.0.0:18080`，浏览器登录和写接口仍关闭。

重复运行同一命令会保留已有 `.env` 和 PostgreSQL 数据卷。检测到已有数据库卷时，脚本在迁移前把自定义格式备份保存到 Git 忽略的 `.local/backups/`，并用 `pg_restore --list` 校验；备份失败则停止升级。每次部署都显式运行一次新的 migration 容器，成功后才启动或更新 API；迁移失败不会重启 API，也不会自动执行 down migration。若数据库卷存在而当前目录没有原 `.env`，脚本会拒绝生成新密码。已有 `.env` 的绑定地址需与本次选择的模式一致；切换为公网预览须先明确修改 `CONTROL_BIND_IP`。启用过浏览器认证的实例应按下文 HTTPS 步骤运维，不使用只读预览脚本覆盖。

当前工作机没有 Docker，因此源码镜像和真实 Compose 启动必须在装有 Docker 的 Linux VPS 上做最终验证。以下发布包流程仍可用于离线构建与传输。

## 1. 构建发布包

在构建机的项目根目录准备 Go 1.27.1、Node.js 22，然后执行：

```sh
./scripts/build-linux-amd64.sh
sh scripts/build-release-archive.sh
```

第一个脚本生成 Linux amd64 控制面、迁移、管理员初始化、节点初始化、Agent 和 Agent 入网令牌程序，并构建 WebUI。第二个脚本生成 `release-<UTC 时间>.tar.gz` 并打印 SHA-256。归档只收录这些二进制、`apps/web/dist`、完整迁移对和预编译镜像所需的 Compose 文件；打包前会检查缺失文件与二进制架构。把归档上传 VPS 后解压到独立版本目录，再进入其 `deployments/compose` 目录操作。发布归档不包含 `.env`、私钥、数据库文件和 macOS 的 `._*` 文件。VPS 需要 Docker Engine 与 Compose 插件。

## 2. 启动控制面和 WebUI

以下命令用于上文的预编译发布包部署。源码一键安装已完成构建与迁移；手动管理源码镜像时，每条 Compose 命令都需添加 `-f compose.source.yaml`（包括登录与 Agent overlay），避免选择发布包专用的 `Dockerfile.prebuilt`。

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

如果使用发布包中的 `node-bootstrap` 在控制面数据库中初始化节点，可用
`CONTROL_NODE_RELAY_PORT` 一并登记中继端口；该字段会参与幂等校验并写入节点记录。只有具备
`forward` 能力的节点才应设置中继端口，且端口必须和代理端口不同：

```sh
docker compose -f compose.yaml run --rm -T \
  -e CONTROL_ADMIN_EMAIL='admin@example.com' \
  -e CONTROL_NODE_GROUP_CODE='RFC.JPT1' \
  -e CONTROL_NODE_GROUP_NAME='Tokyo' \
  -e CONTROL_NODE_GROUP_REGION='JP' \
  -e CONTROL_NODE_NAME='Tokyo 1' \
  -e CONTROL_NODE_REGION='JP' \
  -e CONTROL_NODE_HOST='node.example.com' \
  -e CONTROL_NODE_CAPABILITIES='proxy,forward' \
  -e CONTROL_NODE_PROXY_PORT=443 \
  -e CONTROL_NODE_RELAY_PORT=24443 \
  api /usr/local/bin/node-bootstrap
```

控制台的“创建节点”表单也会执行同样的校验。

### 5.1 控制面准备 Agent TLS

控制面需要专用 CA、服务端证书和私钥，并只通过 HTTPS/mTLS 入口暴露 Agent 路径。服务端证书 SAN 必须覆盖节点实际使用的域名或 IP；浏览器管理入口仍由 Nginx/Caddy 反代到 `127.0.0.1:18080`。控制面示例：

```sh
CONTROL_AGENT_TLS_DIR=/opt/network-control-plane/secrets/agent-tls \
  docker compose -f compose.yaml -f compose.agent-tls.yaml config --quiet
CONTROL_AGENT_TLS_DIR=/opt/network-control-plane/secrets/agent-tls \
  docker compose -f compose.yaml -f compose.agent-tls.yaml up -d --build api
```

独立节点 VPS 需要访问控制面的 18443 端口时，先在 Compose `.env` 中设置 `CONTROL_AGENT_BIND_IP=0.0.0.0`，再执行上面的 `up` 命令，并在服务器防火墙中仅允许已登记节点的来源 IP。保持默认回环绑定时，远程节点无法入网。同机节点继续使用默认的 `127.0.0.1` 绑定。

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

若节点凭据泄露或需要替换 Agent，管理员在 HTTPS WebUI 的节点页执行“撤销 Agent”，再重新签发入网令牌。撤销会立即使现有证书无法通过新的认证检查；已有控制流在下一次身份复核或心跳时断开，节点本地数据监听随之关闭。撤销操作和重新入网记录保留在审计日志中。

### 5.3 同机节点

如果 Agent 与控制面在同一台 VPS，使用 `wss://127.0.0.1:18443/api/v1/agent/stream` 和 `https://127.0.0.1:18443/api/v1/agent/enroll`，并确保服务端证书包含 `127.0.0.1` SAN。独立节点示例中的公网控制面地址不能直接套用到同机回环场景。

```sh
docker compose -f compose.yaml -f compose.proxy-secrets.yaml ps
docker compose -f compose.yaml -f compose.proxy-secrets.yaml logs --tail=100 api
```

升级前先备份 PostgreSQL，再替换发布目录并执行迁移；Compose 不会自动执行 down migration。
