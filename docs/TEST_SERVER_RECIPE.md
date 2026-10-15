# 测试服配方：逐传输 / UDP / 链式 / 端口跳跃的真机验证

这份文档是 036 轮（2026-10-16）实际用过的流程 —— 用**两台按小时计费的临时服务器**
把「每一种协议 × 传输」的真链路矩阵跑了一遍，并抓出了一个真 bug（见
[COMMIT_HISTORY 036](COMMIT_HISTORY.md#036)）。

写下来的理由：这套东西一次就够了吗？不够 —— 以后换内核版本、加传输、改映射，
都要能**照着复现**。下面的脚本与命令都是实际跑通的那一份。

> ⚠️ 两台服务器都是**用完即销毁**的（按小时计费）。别把它们当长期设施，
> 也别在上面留任何真实凭据 —— 本配方里的 UUID / 密码都是**写死的测试值**。

## 0. 需要什么

- 2 台 Linux（本配方在 Ubuntu 26.04 / x86_64 / 2 核上跑过），**全端口放开**，
  有 `sudo`（登录用户不是 root 也行）；
- 客户端就是本仓库的 `dist\picohop-cli.exe`（Windows 侧）；
- 两台机器要有**出站**网络（用来装内核、以及当出口访问目标站点）。

**角色分工**（正因为要验"链式"，两台必须真的互为两端）：

| 角色 | 装什么 | 承担 |
|---|---|---|
| **传输机（前）** | sing-box | vless 的 tcp / ws / grpc / h2 / httpupgrade + vmess 的 ws / grpc；链式时当前置 |
| **UDP 机（后）** | sing-box | hysteria2（+端口跳跃） / shadowsocks / trojan / vmess；链式时当出口 |

## 1. 服务端

把下面这段存成 `srv_setup.sh`，`scp` 到两台机器，各自 `sudo bash srv_setup.sh <角色>`：

```bash
#!/bin/bash
# 用法: sudo bash srv_setup.sh transports|udp
set -uo pipefail
ROLE="${1:-}"
[ "$ROLE" = "transports" ] || [ "$ROLE" = "udp" ] || { echo "角色必须是 transports 或 udp"; exit 2; }
[ "$(id -u)" = "0" ] || { echo "必须用 sudo 跑"; exit 2; }

UUID="11111111-2222-3333-4444-555555555555"   # 测试值，用完随机器一起销毁
SSPW="picohop-ss-test"; TROJANPW="picohop-trojan-test"; HY2PW="picohop-hy2-test"
SNAME="picohop.test"; HOP_LO=20000; HOP_HI=20099

# ---- 装 sing-box：GitHub 直连在部分机器上不通，走镜像
if ! command -v sing-box >/dev/null 2>&1; then
  cd /tmp
  TAG=$(curl -s -m 20 https://ghfast.top/https://api.github.com/repos/SagerNet/sing-box/releases/latest \
        | python3 -c 'import sys,json;print(json.load(sys.stdin)["tag_name"])')
  VER=${TAG#v}
  curl -sL -m 300 -o sb.tgz \
    "https://ghfast.top/https://github.com/SagerNet/sing-box/releases/download/${TAG}/sing-box-${VER}-linux-amd64.tar.gz"
  tar xzf sb.tgz
  install -m 0755 "sing-box-${VER}-linux-amd64/sing-box" /usr/local/bin/sing-box
fi

# ---- 自签证书（h2 / hy2 / trojan 都要 TLS；客户端用 allowInsecure 或 pinSHA256）
mkdir -p /etc/sing-box
[ -s /etc/sing-box/cert.pem ] || openssl req -x509 -newkey rsa:2048 -nodes -days 30 \
  -keyout /etc/sing-box/key.pem -out /etc/sing-box/cert.pem -subj "/CN=${SNAME}" \
  -addext "subjectAltName=DNS:${SNAME}" >/dev/null 2>&1

# ---- 配置
if [ "$ROLE" = "transports" ]; then
  cat > /etc/sing-box/config.json <<JSON
{ "log": { "level": "info", "timestamp": true },
  "inbounds": [
    { "type": "vless", "tag": "vless-tcp", "listen": "::", "listen_port": 10005,
      "users": [ { "uuid": "${UUID}" } ] },
    { "type": "vless", "tag": "vless-ws", "listen": "::", "listen_port": 10001,
      "users": [ { "uuid": "${UUID}" } ], "transport": { "type": "ws", "path": "/ws" } },
    { "type": "vless", "tag": "vless-grpc", "listen": "::", "listen_port": 10002,
      "users": [ { "uuid": "${UUID}" } ], "transport": { "type": "grpc", "service_name": "picohop" } },
    { "type": "vless", "tag": "vless-h2", "listen": "::", "listen_port": 10003,
      "users": [ { "uuid": "${UUID}" } ], "transport": { "type": "http", "host": [ "${SNAME}" ] },
      "tls": { "enabled": true, "server_name": "${SNAME}",
               "certificate_path": "/etc/sing-box/cert.pem", "key_path": "/etc/sing-box/key.pem" } },
    { "type": "vless", "tag": "vless-httpupgrade", "listen": "::", "listen_port": 10004,
      "users": [ { "uuid": "${UUID}" } ],
      "transport": { "type": "httpupgrade", "host": "${SNAME}", "path": "/hup" } },
    { "type": "vmess", "tag": "vmess-ws", "listen": "::", "listen_port": 10006,
      "users": [ { "uuid": "${UUID}", "alterId": 0 } ], "transport": { "type": "ws", "path": "/vws" } },
    { "type": "vmess", "tag": "vmess-grpc", "listen": "::", "listen_port": 10007,
      "users": [ { "uuid": "${UUID}", "alterId": 0 } ],
      "transport": { "type": "grpc", "service_name": "picohopvmess" } }
  ],
  "outbounds": [ { "type": "direct", "tag": "direct" } ] }
JSON
else
  cat > /etc/sing-box/config.json <<JSON
{ "log": { "level": "info", "timestamp": true },
  "inbounds": [
    { "type": "hysteria2", "tag": "hy2", "listen": "::", "listen_port": 443,
      "users": [ { "password": "${HY2PW}" } ],
      "tls": { "enabled": true, "server_name": "${SNAME}",
               "certificate_path": "/etc/sing-box/cert.pem", "key_path": "/etc/sing-box/key.pem" },
      "masquerade": "https://www.bing.com/" },
    { "type": "shadowsocks", "tag": "ss", "listen": "::", "listen_port": 8388,
      "method": "aes-128-gcm", "password": "${SSPW}" },
    { "type": "trojan", "tag": "trojan", "listen": "::", "listen_port": 8443,
      "users": [ { "password": "${TROJANPW}" } ],
      "tls": { "enabled": true, "server_name": "${SNAME}",
               "certificate_path": "/etc/sing-box/cert.pem", "key_path": "/etc/sing-box/key.pem" } },
    { "type": "vmess", "tag": "vmess", "listen": "::", "listen_port": 8080,
      "users": [ { "uuid": "${UUID}", "alterId": 0 } ] }
  ],
  "outbounds": [ { "type": "direct", "tag": "direct" } ] }
JSON
fi
sing-box check -c /etc/sing-box/config.json

# ---- systemd
cat > /etc/systemd/system/sing-box.service <<'UNIT'
[Unit]
Description=sing-box (PicoHop test server)
After=network-online.target
Wants=network-online.target
[Service]
ExecStart=/usr/local/bin/sing-box run -c /etc/sing-box/config.json
Restart=always
RestartSec=2
LimitNOFILE=1000000
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload && systemctl enable --now sing-box
sleep 2 && systemctl is-active sing-box

# ---- 端口跳跃：把一整段 UDP 转到 hy2 的 443（跳跃的服务端侧要件）
if [ "$ROLE" = "udp" ]; then
  IFACE=$(ip -o -4 route show default | awk '{print $5}' | head -1)
  iptables -t nat -C PREROUTING -i "$IFACE" -p udp --dport ${HOP_LO}:${HOP_HI} \
    -j REDIRECT --to-ports 443 2>/dev/null || \
  iptables -t nat -A PREROUTING -i "$IFACE" -p udp --dport ${HOP_LO}:${HOP_HI} \
    -j REDIRECT --to-ports 443
  DEBIAN_FRONTEND=noninteractive apt-get install -y tcpdump >/dev/null 2>&1 || true
fi

ss -tulnp | grep -i sing-box
echo "证书 pin: $(openssl x509 -in /etc/sing-box/cert.pem -noout -fingerprint -sha256 | sed 's/.*=//;s/://g' | tr 'A-Z' 'a-z')"
```

## 2. 客户端节点（用我们自己的 import 建，顺带验一次链接解析）

```powershell
$env:PICOHOP_DATA_DIR = '<一个隔离目录>'   # 绝不指向真实的 data\
$B = '<传输机 IP>'; $A = '<UDP 机 IP>'
$UUID = '11111111-2222-3333-4444-555555555555'
$cli = 'dist\picohop-cli.exe'

& $cli import "vless://$UUID@${B}:10005?type=tcp&security=none#B-tcp"
& $cli import "vless://$UUID@${B}:10001?type=ws&path=%2Fws&security=none#B-ws"
& $cli import "vless://$UUID@${B}:10002?type=grpc&serviceName=picohop&security=none#B-grpc"
& $cli import "vless://$UUID@${B}:10003?type=h2&security=tls&sni=picohop.test&allowInsecure=1#B-h2"
& $cli import "vless://$UUID@${B}:10004?type=httpupgrade&path=%2Fhup&host=picohop.test&security=none#B-hup"
& $cli import "hysteria2://picohop-hy2-test@${A}:443?mport=20000-20099&hop-interval=5&sni=picohop.test&insecure=1#A-hy2-hop"
& $cli import "hysteria2://picohop-hy2-test@${A}:443?pinSHA256=<UDP 机的 pin>&sni=picohop.test#A-hy2-pin"
& $cli import "ss://<base64url(aes-128-gcm:picohop-ss-test)>@${A}:8388#A-ss"
& $cli import "trojan://picohop-trojan-test@${A}:8443?security=tls&sni=picohop.test&allowInsecure=1#A-trojan"
# vmess 用 vmess://base64(JSON)，字段照主流客户端（v/ps/add/port/id/aid/scy/net/type/host/path/tls）
```

> **命名注意**：`-node` 支持"别名的一部分"，所以**节点名之间不能互为子串**
> （`B-tcp` 与 `CHAIN-A-ss-via-B-tcp` 会撞成"匹配到多个节点"）。链式节点直接改
> 隔离配置里的 `front` 字段指向前置节点即可（同名节点会被查重拦下，这是有意的）。

## 3. 跑矩阵

```powershell
$env:PICOHOP_DATA_DIR = '<隔离目录>'
& 'dist\picohop-cli.exe' run -node '<逗号分隔的全部节点名>' -seconds 900   # 另开窗口/后台
# 每个本地端口：
#   TCP：curl -s -m 20 --proxy http://127.0.0.1:<port> http://ip-api.com/json   → 看 query 是不是出口机的 IP
#   UDP：go run ./cmd/udpprobe 127.0.0.1:<port> 223.5.5.5:53,1.1.1.1:53 12      → 同会话两个目标都要有应答
```

**036 轮实测结果**（12 个节点，TCP 与 UDP 各 12 项全绿）：

| 节点 | TCP | UDP | 备注 |
|---|---|---|---|
| vless + tcp / ws / grpc / h2(TLS) / httpupgrade | ✅ | ✅ | h2 走 `allowInsecure`；httpupgrade 曾是红的（映射 bug，见 036） |
| vmess + ws / grpc | ✅ | ✅ | |
| hysteria2（`mport` + `hop-interval=5`） | ✅ | ✅ | 端口跳跃实测见下 |
| hysteria2（`pinSHA256`，自签证书） | ✅ | ✅ | 故意写错 pin 必须失败 ✓ |
| shadowsocks / trojan / vmess | ✅ | ✅ | |
| 链式（前置=传输机的 vless+tcp → 出口=UDP 机的 ss） | ✅ | ✅ | 网络层证据见下 |

**端口跳跃的取证**（服务端抓包，看同一条会话打了哪些目的端口）：

```bash
sudo timeout 95 tcpdump -n -i eth0 'udp and dst portrange 20000-20099'
# 同时客户端经 hy2 节点做一条长连接下载（限速，保证会话活过 hop-interval）：
#   curl.exe -m 45 --limit-rate 400k -o NUL --proxy http://127.0.0.1:<hy2端口> \
#     "https://speed.cloudflare.com/__down?bytes=30000000"
```

036 轮抓到：**同一个源端口（同一条 QUIC 会话）打过 11 个不同目的端口**
（20007/20013/20014/20015/20024/20026/20030/20037/20058/20094/20097）——
既有"首次拨号随机选端口"，也有"建连后每 5 秒换一个端口"。

**链式的网络层取证**（同时跑直连与链式两条下载，在出口机上看对端 IP）：

```bash
sudo ss -tn state established '( sport = :8388 )'
# 036 轮看到两个对端：本机家宽 IP（直连那条）与 <传输机 IP>（链式那条）
# 传输机上则是：10005 的对端 = 本机家宽 IP
```

## 4. 踩过的坑（都真实发生过）

| 坑 | 现象 | 怎么绕 |
|---|---|---|
| GitHub 直连不通 | `curl https://github.com` 返回 000 | 走 `ghfast.top` 镜像（API 与 release 都行） |
| 装的时候忘了 `sudo` | `install: Permission denied`，服务一直 `activating` | 服务端脚本一律 `sudo bash` |
| 脚本经**管道**喂给 ssh | PowerShell 会把行尾变成 CRLF，bash 报 `$'\r': command not found` | 用 `scp` 送文件，落地后再 `sed -i 's/\r$//'` |
| `nohup ... &` 经 ssh 起抓包 | 抓包没起来（ssh 的 stdout 被后台进程继承） | 直接用后台作业把 `tcpdump` 的输出**流回来** |
| `Select-Object -First N` 测退出码 | 管道被提前掐断，`$LASTEXITCODE` 变成假的 0 | 用 `| Out-String` 整段读完再取行 |
| 节点名互为子串 | `-node B-tcp` 报"匹配到多个节点" | 起名避免子串关系（或用 ID） |
| 读配置时撞上 CLI 正在写 | `ConvertFrom-Json` 拿到半截文件，节点"消失" | 读之前先确认那个进程没在动配置 |
| `SupportUDP()` 为真 ≠ 能跑 UDP | 它只看协议自己的 `udp` 开关，不看传输 | 以实测为准（本配方就是干这个的） |

## 5. 收尾

测完**把服务器销毁**（按小时计费）。销毁前不需要保留任何东西 ——
本配方里的凭据都是测试值，服务端配置也在本文件里。
