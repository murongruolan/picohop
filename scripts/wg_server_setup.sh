#!/usr/bin/env bash
# 在一台干净的云服务器上把 WireGuard 服务端搭起来，并输出客户端配置。
#
# 目的：把 PicoHop 的 WireGuard 客户端放到一个**完全可控**的服务端上验证，
# 排除掉"共享服务器/端口映射/防火墙"这些干扰因素。
#
# 用法（在服务器上以 root 执行）：
#   bash wg_server_setup.sh
#
# 前置条件：
#   1) 云服务商的**安全组/防火墙要放行入站 UDP 51820**（这一步只能在控制台做）
#   2) 服务器能访问外网（装包用）
#
# 幂等：重复执行会重建配置（会先把已有的 wg0 关掉）。

set -euo pipefail

WG_PORT="${WG_PORT:-51820}"
WG_IF="${WG_IF:-wg0}"
# 服务端隧道地址与客户端隧道地址
SRV_TUNNEL="${SRV_TUNNEL:-10.200.0.1/24}"
CLI_TUNNEL="${CLI_TUNNEL:-10.200.0.4/32}"
OUT_DIR="${OUT_DIR:-/root/wg-out}"

log() { printf '\n\033[36m=== %s ===\033[0m\n' "$*"; }

log "环境探测"
if [ -f /etc/os-release ]; then . /etc/os-release; echo "OS: ${PRETTY_NAME:-unknown}"; fi
echo "内核: $(uname -r)"
PKG=""
if command -v apt-get >/dev/null 2>&1; then PKG=apt
elif command -v dnf >/dev/null 2>&1; then PKG=dnf
elif command -v yum >/dev/null 2>&1; then PKG=yum
fi
echo "包管理器: ${PKG:-未识别}"

log "安装 wireguard-tools（含 wg / wg-quick；服务端还需要内核模块）"
if ! command -v wg >/dev/null 2>&1; then
  case "$PKG" in
    apt) apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq wireguard wireguard-tools tcpdump iproute2 ;;
    dnf) dnf install -y -q wireguard-tools tcpdump iproute ;;
    yum) yum install -y -q wireguard-tools tcpdump iproute ;;
    *)   echo "请手动安装 wireguard-tools"; exit 1 ;;
  esac
else
  echo "已安装: $(wg --version)"
fi
command -v tcpdump >/dev/null 2>&1 || { case "$PKG" in apt) apt-get install -y -qq tcpdump ;; dnf|yum) $PKG install -y -q tcpdump ;; esac; }

log "生成密钥"
mkdir -p "$OUT_DIR" && chmod 700 "$OUT_DIR"
umask 077
wg genkey > "$OUT_DIR/server.key"
wg pubkey < "$OUT_DIR/server.key" > "$OUT_DIR/server.pub"
wg genkey > "$OUT_DIR/client.key"
wg pubkey < "$OUT_DIR/client.key" > "$OUT_DIR/client.pub"
echo "服务端公钥: $(cat "$OUT_DIR/server.pub")"
echo "客户端公钥: $(cat "$OUT_DIR/client.pub")"

log "写服务端配置 /etc/wireguard/${WG_IF}.conf"
# 先关掉已有接口，保证可重复执行
wg-quick down "$WG_IF" 2>/dev/null || true
mkdir -p /etc/wireguard && chmod 700 /etc/wireguard
cat > "/etc/wireguard/${WG_IF}.conf" <<EOF
[Interface]
Address = ${SRV_TUNNEL}
ListenPort = ${WG_PORT}
PrivateKey = $(cat "$OUT_DIR/server.key")
# 让客户端的流量能出公网（全隧道时必需）
PostUp   = sysctl -w net.ipv4.ip_forward=1; iptables -t nat -A POSTROUTING -s 10.200.0.0/24 -o $(ip route show default | awk '{print $5; exit}') -j MASQUERADE
PostDown = iptables -t nat -D POSTROUTING -s 10.200.0.0/24 -o $(ip route show default | awk '{print $5; exit}') -j MASQUERADE

[Peer]
# PicoHop 客户端
PublicKey = $(cat "$OUT_DIR/client.pub")
AllowedIPs = ${CLI_TUNNEL}
EOF
chmod 600 "/etc/wireguard/${WG_IF}.conf"

log "启动接口"
wg-quick up "$WG_IF"
sysctl -w net.ipv4.ip_forward=1 >/dev/null

log "状态"
wg show
echo
echo "监听情况："
ss -lunp | grep -E "(${WG_PORT}|wg)" || echo "  (未看到 ${WG_PORT}，请检查)"
echo
echo "公网出口 IP（客户端全隧道后应当看到这个）："
curl -s --max-time 8 https://ifconfig.me/ip || curl -s --max-time 8 https://api.ipify.org || echo "  (取不到)"

log "生成客户端配置（复制到 PicoHop 里）"
cat > "$OUT_DIR/client.conf" <<EOF
[Interface]
PrivateKey = $(cat "$OUT_DIR/client.key")
Address = ${CLI_TUNNEL}

[Peer]
PublicKey = $(cat "$OUT_DIR/server.pub")
Endpoint = $(curl -s --max-time 8 https://ifconfig.me/ip || echo "填服务器公网IP"):${WG_PORT}
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
EOF
chmod 600 "$OUT_DIR/client.conf"
cat "$OUT_DIR/client.conf"

log "抓包命令（验证客户端握手包是否真的到达）"
echo "  tcpdump -ni any udp port ${WG_PORT} -X"
echo "  想看握手尝试：watch -n1 'wg show ${WG_IF} latest-handshakes'"

log "完成"
echo "产物目录: $OUT_DIR"
echo "如果客户端仍连不上，先确认云服务商安全组放行了入站 UDP ${WG_PORT}。"
