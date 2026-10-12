# WireGuard 通道探测：定时向目标 UDP 端口发送带标记的包，便于在服务端抓包定位。
#
# 用途：当"握手超时"时，用它区分两类完全不同的故障：
#   · 服务端 tcpdump 能看到这些包  -> 网络与端口映射都正常，问题在 WireGuard 层
#                                    （监听端口 / 公钥 / 预共享密钥）
#   · 服务端 tcpdump 什么都看不到  -> NAT 映射或防火墙把入站 UDP 丢了
#
# 用法：
#   .\scripts\wg_probe.ps1                          # 默认打 203.0.113.10:31496，每秒一个，共 30 个
#   .\scripts\wg_probe.ps1 -Host 1.2.3.4 -Port 51820 -Count 20
#
# 服务端同时执行（容器内注意端口是 51820，宿主机看映射后的端口）：
#   sudo tcpdump -ni any udp port 51820 -X
#   # 或在 NAT 宿主机上：
#   sudo tcpdump -ni any udp port 31496 -X

param(
    [string]$TargetHost = '203.0.113.10',
    [int]$TargetPort = 31496,
    [int]$Count = 30,
    [int]$IntervalMs = 1000
)

$ErrorActionPreference = 'Continue'

Write-Host "===== WireGuard 通道探测 =====" -ForegroundColor Cyan
Write-Host "目标   : $TargetHost`:$TargetPort"
Write-Host "次数   : $Count 个，间隔 $IntervalMs ms"
Write-Host ""
Write-Host "请在服务端同时抓包：" -ForegroundColor Yellow
Write-Host "  容器内   : sudo tcpdump -ni any udp port 51820 -X"
Write-Host "  NAT 宿主 : sudo tcpdump -ni any udp port $TargetPort -X"
Write-Host ""
Write-Host "看得到下面的标记串 => 网络与端口映射正常；看不到 => 入站 UDP 被丢" -ForegroundColor Yellow
Write-Host ""

$udp = New-Object System.Net.Sockets.UdpClient
$udp.Connect($TargetHost, $TargetPort)
$local = $udp.Client.LocalEndPoint
Write-Host "本机源地址: $local" -ForegroundColor Green

$sent = 0
for ($i = 1; $i -le $Count; $i++) {
    # 标记串里带上序号，方便在抓包结果里数清楚到底到了几个
    $tag = "PICOHOP-PROBE-#$i-of-$Count"
    $bytes = [System.Text.Encoding]::ASCII.GetBytes($tag)
    try {
        $udp.Send($bytes, $bytes.Length) | Out-Null
        $sent++
        Write-Host ("  [{0,3}/{1}] 已发送 {2} 字节  {3}" -f $i, $Count, $bytes.Length, $tag)
    } catch {
        Write-Host ("  [{0,3}/{1}] 发送失败：{2}" -f $i, $Count, $_.Exception.Message) -ForegroundColor Red
    }
    Start-Sleep -Milliseconds $IntervalMs
}

Write-Host ""
Write-Host "共发出 $sent 个包。" -ForegroundColor Green
Write-Host "若服务端抓到了这些标记串，说明这条 UDP 通路是好的 —— 那问题就在 WireGuard 层："
Write-Host "  · 容器里 wg 的监听端口（wg show 里的 listening port）是不是 51820"
Write-Host "  · 服务端 peer 的公钥是不是客户端这个（wg show 里的 peer: 一行）"
Write-Host "  · 服务端 peer 是否配了 PresharedKey 而客户端没填"
$udp.Close()
