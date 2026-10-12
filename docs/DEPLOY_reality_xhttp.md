# 部署记录：VLESS + Reality + xHTTP（203.0.113.10）

> 部署时间：2026-10-11
> 目标机器：NAT 服务器容器 `US-ACO1`（Debian 13，内网 `172.16.233.90`）
> 用途：TCP-only 线路上的强混淆代理出口（1~2 个客户端）

---

## 1. 为什么选这个方案

用户线路的现实约束：**供应商的端口映射只转发 TCP，UDP 不生效**
（已用「容器内开 UDP 监听 + 抓包」证实；详见 `PROJECT_PROGRESS.md`）。
而 WireGuard 只用 UDP，所以「客户端直连出口节点」这条路走不通。

于是改为在出口节点上直接跑 **Xray VLESS + Reality + xHTTP（SplitHTTP）**：

| 需求 | Reality + xHTTP |
|---|---|
| 只能走 TCP | ✅ 纯 TCP |
| 强加密 | ✅ X25519 + AEAD（VLESS） |
| 强混淆 / 抗主动探测 | ✅ 借用真实站点 `www.amazon.com` 的 TLS，探测者看到的是真站 |
| 不需要域名证书 | ✅ 零申请成本 |
| 速度影响小 | ✅ 实测**比直连还快**（见下方测速） |
| 客户端方便 | ✅主流客户端（Xray 内核）原生支持 |

xHTTP 相比裸 TCP 的好处：走 HTTP 语义（可挂 CDN）、**长连接复用**（后续请求无重复握手开销）。

---

## 2. 服务端部署

### 2.1 安装（233boy / Xray 一键脚本）

```bash
apt-get install -y unzip          # 脚本需要
curl -fsSL https://raw.githubusercontent.com/233boy/Xray/master/install.sh -o /tmp/x233.sh
bash /tmp/x233.sh                 # 非交互，装完自动生成一个默认 Reality 入站
```

装完目录结构：

```
/etc/xray/bin/xray                核心
/etc/xray/config.json             基底（log/api/policy/routing/outbounds）
/etc/xray/conf/*.json             入站，一个文件一个入站
/usr/local/bin/xray               管理脚本入口
systemd: xray.service
   ExecStart=/etc/xray/bin/xray run -config /etc/xray/config.json -confdir /etc/xray/conf
```

### 2.2 关键坑：脚本不支持 Reality + xHTTP 组合

233boy 脚本里 `add` 的 Reality 分支**写死了 TCP**：

```bash
[[ $is_reality ]] && net=reality        # 第 513 行
# 生成链接时也固定 type=tcp&flow=xtls-rprx-vision   # 第 1550 行
```

所以**用脚本装核心与服务，然后手写入站配置**。

另外：xHTTP 的 `flow` **必须留空** —— `xtls-rprx-vision` 只适用于裸 TCP，配到 xHTTP 上会启动失败。

### 2.3 实际入站配置

`/etc/xray/conf/VLESS-REALITY-XHTTP-30022.json`（监听内部 `51821`，对应公网 `30022`）：

```json
{
  "inbounds": [{
    "tag": "VLESS-REALITY-XHTTP-30022",
    "port": 51821,
    "listen": "0.0.0.0",
    "protocol": "vless",
    "settings": {
      "clients": [{ "id": "<UUID>", "flow": "" }],
      "decryption": "none"
    },
    "streamSettings": {
      "network": "xhttp",
      "security": "reality",
      "realitySettings": {
        "dest": "www.amazon.com:443",
        "serverNames": ["www.amazon.com", ""],
        "publicKey": "<PUBLIC_KEY>",
        "privateKey": "<PRIVATE_KEY>",
        "shortIds": ["<SHORT_ID>"]
      },
      "xhttpSettings": { "path": "<PATH>", "host": "www.amazon.com", "mode": "auto" }
    },
    "sniffing": { "enabled": true, "destOverride": ["http","tls"], "routeOnly": true }
  }]
}
```

同时保留一份监听 `443` 的同样入站（公网 443 目前被 CDN 占着，见第 5 节；
一旦用户把某个公网端口映射到内部 443，就能直接获得更自然的端口伪装）。

改完必须**先自检再重启**：

```bash
/etc/xray/bin/xray run -test -config /etc/xray/config.json -confdir /etc/xray/conf
systemctl restart xray
ss -ltnp | grep xray
```

---

## 3. 客户端参数（**属于凭据，勿外传**）

### 3.1 双出口：direct + WARP

对应 WarpPool 的 `dual` 模式 —— **两个入站，按入站标签分流到不同出站**：

| 出口 | 内部端口 | 公网端口 | 出站 | 出口 IP |
|---|---|---|---|---|
| **direct**（NAT 自身网络） | `51821` | `30022` | `freedom` | `203.0.113.10` |
| **WARP**（Cloudflare） | `51822` | `30023` | `socks → 127.0.0.1:40000` | `104.28.x.x`（Cloudflare） |

> 早期曾把 WARP 入站放在内部 `51820`(TCP)（与 WireGuard 的 UDP 51820 只是数字撞号、
> 协议不同、互不影响），现按用户要求改为**独立编号** `51822`，避免以后混淆。

分流实现（加在 `/etc/xray/config.json`）：

```jsonc
// outbounds 追加
{ "tag": "warp", "protocol": "socks",
  "settings": { "servers": [ { "address": "127.0.0.1", "port": 40000 } ] } }

// routing.rules **放在最前面**（Xray 第一条匹配生效）
{ "type": "field", "inboundTag": ["VLESS-REALITY-XHTTP-WARP-51822"], "outboundTag": "warp" },
{ "type": "field", "inboundTag": ["VLESS-REALITY-XHTTP-30022"],      "outboundTag": "direct" }
```

`127.0.0.1:40000` 是 `warp-svc` 提供的 SOCKS5（与 WarpPool 的 redsocks 用的是同一个）。

**服务端访问日志可见分流确实生效：**

```
accepted tcp:api.ipify.org:443  [VLESS-REALITY-XHTTP-30022 -> direct]
accepted tcp:api.ipify.org:443  [VLESS-REALITY-XHTTP-WARP-51822 -> warp]
```

### 3.2 公共参数（两条链路完全一致，只有地址端口不同）

```
协议     : VLESS
UUID     : ed401d40-9c96-4217-8e6b-ab6ac21169c1
流控flow : （留空！xHTTP 不能填 xtls-rprx-vision）
传输     : xhttp
路径path : /63ffaa1022cf
主机host : www.amazon.com
模式mode : auto
安全     : reality
SNI      : www.amazon.com
指纹fp   : chrome
公钥pbk  : j9hlAZUR9UIfQJdv8Ti6lW07zonxbYOjJufVxILOTTw
短ID sid : d423dbf66168796e
spiderX  : /
```

主流客户端导入用（复制整行 → 「从剪贴板导入批量 URL」）：

**① 直连出口 `203.0.113.10:30022`**

```
vless://ed401d40-9c96-4217-8e6b-ab6ac21169c1@203.0.113.10:30022?encryption=none&security=reality&sni=www.amazon.com&fp=chrome&pbk=j9hlAZUR9UIfQJdv8Ti6lW07zonxbYOjJufVxILOTTw&sid=d423dbf66168796e&spx=%2F&type=xhttp&path=%2F63ffaa1022cf&host=www.amazon.com&mode=auto#NAT-direct
```

**② WARP 出口 `203.0.113.10:30023`**

```
vless://ed401d40-9c96-4217-8e6b-ab6ac21169c1@203.0.113.10:30023?encryption=none&security=reality&sni=www.amazon.com&fp=chrome&pbk=j9hlAZUR9UIfQJdv8Ti6lW07zonxbYOjJufVxILOTTw&sid=d423dbf66168796e&spx=%2F&type=xhttp&path=%2F63ffaa1022cf&host=www.amazon.com&mode=auto#NAT-WARP
```

（两条链接另存于 `.cache/xray-test/share-link-direct.txt` / `share-link-warp.txt`，该目录被 gitignore。）

---

## 4. 验证结果

### 4.1 抗主动探测

未认证的 TLS 客户端访问该端口时，Reality 会把连接**原样转发到真实站点**：

```
容器本机: curl -k https://127.0.0.1:51821/ -H "Host: www.amazon.com"  -> HTTP 200
从家宽  : curl -k https://203.0.113.10:30022/ -H "Host: www.amazon.com" -> HTTP 200
不带 Host 头（Amazon 会拒）                                          -> HTTP 403
```

即：探测者拿到的是**真 Amazon 的响应**，与直接访问 Amazon 无法区分。

### 4.2 真实代理（从家宽经 Xray 客户端）

**双出口实测（一个客户端进程，两个本地端口分别走两条链路）：**

| 链路 | 出口 IPv4 | 地区 / ISP | 真实网站 |
|---|---|---|---|
| direct（30022） | `203.0.113.10` | United States / System In Place | example.com HTTP 200 ✅ |
| WARP（30023） | `203.0.113.28` | United States / **Cloudflare, Inc.** | example.com HTTP 200 ✅ |

服务端访问日志佐证分流正确：

```
accepted tcp:api.ipify.org:443  [VLESS-REALITY-XHTTP-30022 -> direct]
accepted tcp:api.ipify.org:443  [VLESS-REALITY-XHTTP-WARP-51822 -> warp]
```

单出口（direct）的早期记录：

```
出口 IP:
  api.ipify.org  -> 203.0.113.10
  ip-api.com     -> United States | System In Place | 203.0.113.10
  ifconfig.me    -> 2001:470:1f05:45e:...    ← 走了节点的 IPv6 出口
真实网站:
  example.com    -> "Example Domain"
```

### 4.3 测速（10 MB，Cloudflare）

| 路径 | 耗时 | 速度 |
|---|---|---|
| 直连 | 2.914 s | 3.60 MB/s |
| **经 xHTTP+Reality** | **2.567 s** | **4.08 MB/s** |

**经隧道反而快 13%** —— 因为家宽直连 Cloudflare 本身拥堵，而节点走 HE IPv6 优质路由。
xHTTP + Reality 自身的开销几乎可以忽略。

---

## 5. 已知限制与后续建议

### 5.1 公网 443 被 CDN 占用

`203.0.113.10:443` 返回：

```
HTTP/1.1 418 I'm a Teapot
Server: TencentEdgeOne
```

**那是腾讯云 EdgeOne CDN 的响应，不是本容器**（这也是为什么最初扫端口时"443 开、22/80 不通"）。
因此：

- **不能**直接用公网 443；
- 想要更自然的端口伪装，可在供应商面板加一条 **`公网 8443 → 内部 443`**（内部 443 的入站已经配好，加完即可用）；
- 更彻底的做法：如果 EdgeOne 上有域名，把该域名指到本容器 —— 那是**真 CDN + 真证书**，伪装强度最高（此时通常用普通 TLS 而非 Reality）。

### 5.2 出口 IP 不是住宅 IP

`203.0.113.10` 归属美国机房（`System In Place`）。**WARP 出口是 `104.28.x.x`（Cloudflare）**，
两条链路都**不是住宅 IP**。若需要特定地区/住宅出口，需要换出口节点或另接上游。

### 5.2.1 WARP 出口的 UDP 可能不可用

direct 入站的出站是 `freedom`，TCP/UDP 都支持；
WARP 入站的出站是 `socks → warp-svc:40000`，**取决于 WARP 的 SOCKS5 是否支持 UDP ASSOCIATE**。
浏览器走 QUIC(UDP/443) 若失败会自动回退 TCP，**网页浏览不受影响**；
如果发现某些依赖 UDP 的应用异常，可以给该入站单独加一条出站规则。

### 5.3 IPv6 出口会导致出口 IP 不一致

节点有 HE IPv6，`freedom` 出站会优先走 IPv6，于是同一客户端不同目标可能看到
IPv4/IPv6 两个不同出口。若希望出口 IP 始终是 `203.0.113.10`，
给出站加 `"domainStrategy": "UseIPv4"`，或直接加一条阻断 IPv6 的路由规则。

### 5.4 233boy 脚本自带的路由规则

基底 `config.json` 里有这些默认规则，可能不符合预期：

```
geoip:cn     -> block     ← 把中国 IP 的流量直接黑洞（防止被当作回国代理滥用）
geosite:openai -> direct  ← OpenAI 走直连（不是走代理）
geoip:private -> block
```

客户端若把国内站点也丢进代理会被拦；建议在客户端侧配好分流，
或按需修改 `/etc/xray/config.json` 的 `routing.rules`。

> ⚠️ 用 `xray` 菜单命令改动配置时，脚本可能按它自己的模板重写入站文件
> （它不认识 xHTTP + Reality 组合）。**手写的入站配置请自行备份**：
> `/etc/xray/conf/VLESS-REALITY-XHTTP-*.json`

---

## 6. 复现/复查命令

```bash
# 服务端
systemctl status xray
ss -ltnp | grep xray
/etc/xray/bin/xray run -test -config /etc/xray/config.json -confdir /etc/xray/conf
tail -f /var/log/xray/error.log
cat /root/.xhttp_shortid /root/.xhttp_path      # 本次生成的 shortId / path

# 从家宽验证（无需客户端，验证 Reality 是否在正常工作）
curl -k -o /dev/null -w "%{http_code}\n" https://203.0.113.10:30022/ -H "Host: www.amazon.com"
# 期望 200
```
