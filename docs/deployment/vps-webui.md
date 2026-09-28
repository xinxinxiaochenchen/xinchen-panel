# VPS 正式版部署

本项目通过浏览器使用。控制面、React WebUI 和 PostgreSQL 用 Docker Compose 运行，节点数据面由独立 Go Agent 执行。管理入口支持纯 IP HTTP，HTTPS 自行选择；Agent mTLS 和 Trojan TLS 分别配置。本说明适用于 Linux VPS。

## 1. 源码一键安装

安装 curl、Git、Docker Engine 和 Compose 插件（[Docker 安装说明](https://docs.docker.com/engine/install/)），以 root 运行：

```sh
curl -fsSL https://raw.githubusercontent.com/xinxinxiaochenchen/xinchen-panel/main/scripts/install-vps.sh -o xinchen-panel-install.sh && sh xinchen-panel-install.sh --public-http
```

首次输入管理员邮箱和 12–72 字节密码，密码不会回显。安装器生成随机数据库密码和代理凭据密钥、构建前后端、应用迁移、初始化管理员并启动服务。访问 `http://服务器IP:18080/` 登录；服务器及云平台防火墙需允许 TCP 18080。安装目录默认 `/opt/xinchen-panel`，可用 `XINCHEN_PANEL_DIR=/absolute/path` 指定。

| 参数 | 用途 |
| --- | --- |
| `--public-http` | 可操作的 IP HTTP 面板，绑定 `0.0.0.0:18080` |
| `--https` | 可操作面板，绑定 `127.0.0.1:18080`，使用 Secure Cookie，自行配置 HTTPS 反代 |
| `--public-preview` | 只读 HTTP 预览，关闭账户和写操作 |
| 无参数 | 首次使用 HTTP；已有安装保留原模式 |

HTTP 使用 `control_session`/`control_csrf` host-only Cookie，HTTPS 使用 Secure `__Host-control_session`/`__Host-control_csrf`。两种模式均验证账户、权限、CSRF 和浏览器请求来源。HTTPS 模式应通过 HTTPS 入口访问。

不需要在 VPS 安装 Go 或 Node.js。首次编译需访问 GitHub、npm、Go 模块和镜像仓库，建议为构建准备至少 2 GiB 可用内存；低内存机器可采用第 2 节发布包方式。安装器检查依赖，不自动升级系统 Docker。

### 无人值守安装

准备仅当前管理员可读的绝对路径密码文件，权限 `0600` 或 `0400`。文件只放一行密码，不将密码写入命令参数、`.env` 或 Git：

```sh
CONTROL_ADMIN_EMAIL='owner@example.com' \
CONTROL_ADMIN_PASSWORD_FILE=/root/private/panel-admin-password \
  sh xinchen-panel-install.sh --public-http
```

安装后自行保管或移除初始密码文件。后续更新根据数据库中的管理员状态跳过初始化，不读取密码文件、不修改现有管理员密码。初始化失败时 API 不启动；修正输入后再次执行即可。

## 2. 预编译发布包

构建机准备 Go 1.27.1 和 Node.js 22：

```sh
sh scripts/build-linux-amd64.sh
sh scripts/build-release-archive.sh
```

归档包含 Linux amd64 程序、WebUI、全部迁移、Compose 文件及部署说明，不包含数据库、`.env`、密钥或 AppleDouble 文件。解压到独立版本目录，进入 `deployments/compose`。以下命令针对发布包；源码 checkout 手动执行 API Compose 命令时需额外加入 `-f compose.source.yaml`。

准备持久密钥（发布目录之外）：

```sh
install -d -o 65532 -g 65532 -m 0700 /opt/xinchen-panel/secrets/proxy
openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n' > /opt/xinchen-panel/secrets/proxy/proxy.key
chmod 0600 /opt/xinchen-panel/secrets/proxy/proxy.key
chown 65532:65532 /opt/xinchen-panel/secrets/proxy/proxy.key
```

复制 `.env.example` 为 `.env`，权限 `0600`，写入真实配置：

```dotenv
POSTGRES_PASSWORD=替换为openssl_rand_hex_32生成的随机十六进制密码
CONTROL_DEPLOYMENT_MODE=http
CONTROL_BIND_IP=0.0.0.0
CONTROL_BROWSER_AUTH_ENABLED=true
CONTROL_BROWSER_COOKIE_SECURE=false
CONTROL_PROXY_CREDENTIAL_SECRET_DIR=/opt/xinchen-panel/secrets/proxy
```

启动数据库并迁移，初始化管理员，最后启动 API：

```sh
docker compose -f compose.yaml -f compose.proxy-secrets.yaml build migrate api
docker compose -f compose.yaml -f compose.proxy-secrets.yaml up -d --wait db
docker compose -f compose.yaml -f compose.proxy-secrets.yaml run --rm -T --no-deps migrate
read -r -s -p '管理员初始密码: ' PANEL_INITIAL_PASSWORD
printf '\n'
printf '%s\n' "$PANEL_INITIAL_PASSWORD" | docker compose -f compose.yaml -f compose.proxy-secrets.yaml \
  run --rm -T --no-deps -e CONTROL_ADMIN_EMAIL='owner@example.com' \
  api /usr/local/bin/admin-bootstrap --if-needed
unset PANEL_INITIAL_PASSWORD
docker compose -f compose.yaml -f compose.proxy-secrets.yaml up -d --no-deps api
```

以上 `read -s -p` 示例使用 Bash。`admin-bootstrap --status` 输出 `configured` 或 `empty`；`--if-needed` 保留已有管理员。普通 `admin-bootstrap` 可显式创建额外管理员。

## 3. 登录后的配置顺序

1. 管理页创建资源域和节点，登记地址、能力及代理/中继端口。
2. 按第 5 节启用控制面的 Agent 入口，并让节点 Agent 入网；节点页查看在线与配置状态。
3. 创建共享线路；多跳每一跳登记中继端口，并启用持久线路密钥。
4. 创建用户和套餐，将节点资源域、线路、额度与限制授予用户；创建有效套餐授权。
5. 转发业务先配置管理员目标策略，再由用户创建 TCP/UDP 转发规则。
6. 代理业务由用户创建代理连接、订阅及可选分流 Profile。Agent 配置应用成功后，订阅才导出可用目标。

管理页可维护用户、节点资料及启停、套餐生命周期、授权取消、角色、规则集、Agent 撤销、流量统计与审计。用户可维护自有线路、转发、代理连接、订阅、分流及密码。新库没有示例业务数据，空列表表示尚未配置。

## 4. 可选 HTTPS 管理入口

源码安装运行 `sh scripts/deploy-vps.sh --https`，将面板改为回环绑定和 Secure Cookie，再用 Nginx/Caddy 反代到 `http://127.0.0.1:18080`。反代保留 Host，并按需要传递 WebSocket Upgrade。浏览器访问你的 HTTPS 地址。

发布包手动部署修改 `.env`：

```dotenv
CONTROL_DEPLOYMENT_MODE=https
CONTROL_BIND_IP=127.0.0.1
CONTROL_BROWSER_AUTH_ENABLED=true
CONTROL_BROWSER_COOKIE_SECURE=true
```

加载原有密钥及 Agent/relay overlay 后重建 API。切换模式后重新登录。改回 HTTP 时源码安装运行 `sh scripts/deploy-vps.sh --public-http`；发布包改回第 2 节配置。管理 HTTPS 不影响节点使用的 CA 或已有 Agent 身份。

## 5. 节点 Agent

### 5.1 控制面专用 TLS 入口

管理 WebUI 可以使用 HTTP；节点连接控制面的专用 18443 TLS/mTLS 入口。准备 CA 和带实际控制面 IP 或域名 SAN 的服务端证书。以下 Bash 示例为 IPv4 控制面创建私有 CA，节点通过显式 CA 文件信任它：

```sh
PANEL_CONTROL_IP=203.0.113.10
PANEL_TLS_DIR=/opt/xinchen-panel/secrets/agent-tls
install -d -m 0700 "$PANEL_TLS_DIR"
openssl genpkey -algorithm ED25519 -out "$PANEL_TLS_DIR/agent-ca.key"
openssl req -new -x509 -key "$PANEL_TLS_DIR/agent-ca.key" -days 3650 \
  -subj '/CN=Panel Agent CA' -addext 'basicConstraints=critical,CA:TRUE' \
  -addext 'keyUsage=critical,keyCertSign,cRLSign' -out "$PANEL_TLS_DIR/agent-ca.crt"
openssl genpkey -algorithm ED25519 -out "$PANEL_TLS_DIR/server.key"
openssl req -new -key "$PANEL_TLS_DIR/server.key" -subj '/CN=Panel Agent Control' -out "$PANEL_TLS_DIR/server.csr"
printf 'basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=serverAuth\nsubjectAltName=IP:%s,IP:127.0.0.1\n' "$PANEL_CONTROL_IP" > "$PANEL_TLS_DIR/server.ext"
openssl x509 -req -in "$PANEL_TLS_DIR/server.csr" -CA "$PANEL_TLS_DIR/agent-ca.crt" \
  -CAkey "$PANEL_TLS_DIR/agent-ca.key" -CAcreateserial -days 365 \
  -extfile "$PANEL_TLS_DIR/server.ext" -out "$PANEL_TLS_DIR/server.crt"
chmod 0600 "$PANEL_TLS_DIR/agent-ca.key" "$PANEL_TLS_DIR/server.key"
chown -R 65532:65532 "$PANEL_TLS_DIR"
```

将 `203.0.113.10` 替换为控制面真实 IP。域名入口在 `subjectAltName` 使用 `DNS:控制面域名`；IPv6 使用对应 IP SAN 与 URL 方括号。不要覆盖已使用的 CA：节点身份和中继证书依赖原 CA。保管 CA 私钥，节点只接收 `agent-ca.crt`。

在控制面 **持久 `.env`** 中保存：

```dotenv
CONTROL_AGENT_TLS_DIR=/opt/xinchen-panel/secrets/agent-tls
CONTROL_AGENT_BIND_IP=0.0.0.0
```

源码安装再运行 `sh scripts/deploy-vps.sh`，安装器会保留浏览器模式并加载 Agent TLS overlay；后续更新继续保留。发布包加载所有已启用 overlay：

```sh
docker compose -f compose.yaml -f compose.proxy-secrets.yaml -f compose.agent-tls.yaml up -d --build api
```

仅同机 Agent 可保持 `CONTROL_AGENT_BIND_IP=127.0.0.1`；独立节点需能访问控制面 TCP 18443。实际 enroll、renew 和 stream 仅挂载于专用 TLS 入口，管理面仍可签发入网令牌和撤销 Agent。

### 5.2 多跳持久密钥

多跳需一次性创建独立线路密钥，不在升级时重建：

```sh
install -d -o 65532 -g 65532 -m 0700 /opt/xinchen-panel/secrets/relay
openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n' > /opt/xinchen-panel/secrets/relay/relay.key
chmod 0600 /opt/xinchen-panel/secrets/relay/relay.key
chown 65532:65532 /opt/xinchen-panel/secrets/relay/relay.key
```

持久 `.env` 加入 `CONTROL_RELAY_SECRET_DIR=/opt/xinchen-panel/secrets/relay`，源码安装运行 `sh scripts/deploy-vps.sh`；发布包在所有 API 命令中再加 `-f compose.relay-secrets.yaml`。单跳可省略该配置。

### 5.3 节点 VPS 入网

节点可解压预编译发布包；也可克隆源码，选择源码 Agent Dockerfile，不需要主机 Go：

```sh
git clone https://github.com/xinxinxiaochenchen/xinchen-panel.git
cd xinchen-panel/deployments/compose
export CONTROL_AGENT_DOCKERFILE=deployments/compose/Dockerfile.agent.source
```

预编译发布包使用默认 `Dockerfile.agent`，省略上面 export。创建持久凭据和状态目录，将控制面的 CA 公钥复制进去：

```sh
export CONTROL_AGENT_CREDENTIAL_DIR=/opt/xinchen-panel/secrets/agent-credentials
export CONTROL_AGENT_STATE_DIR=/opt/xinchen-panel/state/agent
install -d -o 65532 -g 65532 -m 0700 "$CONTROL_AGENT_CREDENTIAL_DIR" "$CONTROL_AGENT_STATE_DIR"
cp /你的传入目录/agent-ca.crt "$CONTROL_AGENT_CREDENTIAL_DIR/agent-ca.crt"
chmod 0644 "$CONTROL_AGENT_CREDENTIAL_DIR/agent-ca.crt"
chown 65532:65532 "$CONTROL_AGENT_CREDENTIAL_DIR/agent-ca.crt"
export CONTROL_AGENT_NODE_ID='<节点页UUID>'
export CONTROL_AGENT_STREAM_URL='wss://203.0.113.10:18443/api/v1/agent/stream'
export CONTROL_AGENT_ENROLL_URL='https://203.0.113.10:18443/api/v1/agent/enroll'
export CONTROL_AGENT_VERSION='0.1.0'
docker compose -f compose.agent-local.yaml build agent
```

在节点管理页输入当前密码签发令牌，复制到节点终端的静默输入（以下为 Bash）：

```sh
read -r -s -p '一次性入网令牌: ' PANEL_ENROLL_TOKEN
printf '\n'
printf '%s\n' "$PANEL_ENROLL_TOKEN" | docker compose -f compose.agent-local.yaml run --rm -T agent enroll
unset PANEL_ENROLL_TOKEN
docker compose -f compose.agent-local.yaml up -d agent
```

令牌 10 分钟有效且仅可使用一次。Agent 在本机生成私钥，收到 24 小时客户端证书后主动连接控制面，自动续签；私钥不离开节点。

需要代理能力时，将代理客户端信任的 `proxy.crt` 和 `proxy.key` 放入凭据目录，私钥权限 `0600`、所有者 UID 65532，并设置：

```dotenv
CONTROL_AGENT_PROXY_CERT_FILE=/run/agent/proxy.crt
CONTROL_AGENT_PROXY_KEY_FILE=/run/agent/proxy.key
```

需要中继能力时设置登记的节点地址/端口及可写证书路径，Agent 自动申请中继证书：

```dotenv
CONTROL_AGENT_RELAY_HOST=节点登记的IP或域名
CONTROL_AGENT_RELAY_PORT=24443
CONTROL_AGENT_RELAY_CERT_FILE=/run/agent/relay.crt
CONTROL_AGENT_RELAY_KEY_FILE=/run/agent/relay.key
```

把节点的上述配置持久保存到节点 Compose `.env`，使重启和更新不依赖终端 export。凭据/状态目录放在发布目录之外。Agent 使用 host network，代理、转发和中继端口直接使用节点 VPS 端口，需与节点配置及防火墙一致。

同机 Agent 使用 `wss://127.0.0.1:18443/api/v1/agent/stream` 和 `https://127.0.0.1:18443/api/v1/agent/enroll`，服务端证书需有 `127.0.0.1` SAN。

### 5.4 撤销与重新入网

管理页撤销旧 Agent，然后在节点停止进程，保留状态目录并移走旧活动证书/私钥，再签发新令牌入网：

```sh
docker compose -f compose.agent-local.yaml stop agent
PANEL_OLD_IDENTITY_DIR=$(mktemp -d /root/panel-old-identity-XXXXXX)
mv "$CONTROL_AGENT_CREDENTIAL_DIR/agent.crt" "$CONTROL_AGENT_CREDENTIAL_DIR/agent.key" "$PANEL_OLD_IDENTITY_DIR/"
```

旧密钥按你的凭据保管策略处理。保留 `CONTROL_AGENT_STATE_DIR` 中的额度租约、用量 outbox 和状态，不清空它们；CA 公钥和代理证书保留。再执行 5.3 的新令牌入网和启动命令。入网程序拒绝覆盖已有活动身份，所以必须先停止并移走旧身份文件。

## 6. 更新与备份

源码安装再次运行安装命令，无参数保留 HTTP/HTTPS/预览模式。安装器只快进更新同仓库、干净 `main` 分支，遇到本地源码改动会停止；`.env`、`.local` 和数据卷不进入 Git。已有数据库先备份至 `.local/backups` 并检查归档索引，备份或迁移失败不重启 API。丢失 `.env` 或已有加密密钥时先恢复原文件，安装器不为已有数据库生成新密码/新密钥。

预编译版本手动升级需备份 PostgreSQL、保存原 `.env` 和密钥目录，替换发布目录后执行 migration，再加载同一组 overlay 更新 API。不要删除 `network-control-plane_postgres_data` 数据卷；不会自动执行 down migration。

管理 HTTPS、Agent CA 和多跳密钥均独立。升级时保留 `.env` 中的 `CONTROL_AGENT_TLS_DIR`、`CONTROL_RELAY_SECRET_DIR` 及其原始文件，避免意外停掉节点连接或更换已有密钥。
