package proxy

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// httpConnect 在已经建立好的 TCP 连接上发起 HTTP CONNECT 隧道请求（RFC 7230 §4.3.6）。
//
// 这是"第二次握手"的 HTTP 版本：连接本身可能已经被前置代理隧道化，
// 但 HTTP 层对此完全无感——它只是在一条字节流上写一段文本而已。
//
// 调用前请先给 conn 设置好 deadline。
func httpConnect(conn net.Conn, host string, port int, username, password string) error {
	authority := net.JoinHostPort(host, strconv.Itoa(port))

	var sb strings.Builder
	sb.WriteString("CONNECT ")
	sb.WriteString(authority)
	sb.WriteString(" HTTP/1.1\r\nHost: ")
	sb.WriteString(authority)
	sb.WriteString("\r\n")
	if username != "" || password != "" {
		sb.WriteString("Proxy-Authorization: Basic ")
		sb.WriteString(base64.StdEncoding.EncodeToString([]byte(username + ":" + password)))
		sb.WriteString("\r\n")
	}
	// 明确要求隧道建立后不再复用这条 TCP，避免上游连接池语义歧义。
	sb.WriteString("Proxy-Connection: Keep-Alive\r\n\r\n")

	if _, err := conn.Write([]byte(sb.String())); err != nil {
		return fmt.Errorf("CONNECT 请求写入失败: %w", err)
	}

	br := bufio.NewReaderSize(conn, 4096)
	head, err := readHTTPHead(br)
	if err != nil {
		return fmt.Errorf("CONNECT 应答读取失败: %w", err)
	}
	if head.status != 200 {
		// 有些代理返回 2xx 之外的等价码（如 405 也算失败），统一按失败处理
		return fmt.Errorf("CONNECT 被拒绝：%s", head.statusLine)
	}
	return nil
}

// ------------------------------------------------------------------
// HTTP 请求头解析（服务"普通代理请求"与"CONNECT"两种形态）
// ------------------------------------------------------------------

// httpHead 是一条 HTTP 请求头或应答头。
type httpHead struct {
	// 请求行三段；应答行则 first="HTTP/1.1" second="200"
	first, second, third string
	status               int
	statusLine           string
	method               string
	target               string
	version              string

	header map[string][]string
	raw    []byte // 原始字节（含结尾空行），转发时可原样复用
}

// Get 返回首个同名头的值。
func (h *httpHead) Get(key string) string {
	v := h.header[strings.ToLower(key)]
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// Set 设置头（覆盖同名）。
func (h *httpHead) Set(key, value string) {
	lk := strings.ToLower(key)
	h.header[lk] = []string{value}
}

// Del 删除头。
func (h *httpHead) Del(key string) {
	delete(h.header, strings.ToLower(key))
}

// Has 判断头是否存在。
func (h *httpHead) Has(key string) bool {
	_, ok := h.header[strings.ToLower(key)]
	return ok
}

// Bytes 把头重新序列化。
func (h *httpHead) Bytes() []byte {
	var sb strings.Builder
	sb.Write(h.raw)
	return []byte(sb.String())
}

// readHTTPHead 从 br 读取完整的一组 HTTP 头（直到空行）。
func readHTTPHead(br *bufio.Reader) (*httpHead, error) {
	h := &httpHead{header: map[string][]string{}}

	line, err := readLine(br)
	if err != nil {
		return nil, err
	}
	if line == "" {
		return nil, io.ErrUnexpectedEOF
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return nil, fmt.Errorf("首行格式错误: %q", line)
	}
	h.first, h.second = parts[0], parts[1]
	if len(parts) >= 3 {
		h.third = parts[2]
	}
	if len(parts[0]) >= 5 && parts[0][:5] == "HTTP/" { // 应答
		h.statusLine = line
		h.status, _ = strconv.Atoi(parts[1])
	} else { // 请求
		h.method, h.target, h.version = parts[0], parts[1], "HTTP/1.1"
		if len(parts) >= 3 {
			h.version = parts[2]
		}
	}
	h.raw = append(h.raw, line...)
	h.raw = append(h.raw, '\r', '\n')

	for {
		line, err := readLine(br)
		if err != nil {
			return nil, err
		}
		h.raw = append(h.raw, line...)
		h.raw = append(h.raw, '\r', '\n')
		if line == "" {
			break
		}
		if idx := strings.IndexByte(line, ':'); idx > 0 {
			key := strings.ToLower(strings.TrimSpace(line[:idx]))
			val := strings.TrimSpace(line[idx+1:])
			h.header[key] = append(h.header[key], val)
		}
	}
	return h, nil
}

// readLine 读一行（不含 CRLF）。容忍裸 LF。
func readLine(br *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		b, err := br.ReadByte()
		if err != nil {
			if sb.Len() > 0 && err == io.EOF {
				return sb.String(), nil
			}
			return "", err
		}
		if b == '\n' {
			s := sb.String()
			return strings.TrimSuffix(s, "\r"), nil
		}
		sb.WriteByte(b)
		if sb.Len() > 64*1024 {
			return "", fmt.Errorf("HTTP 头行过长")
		}
	}
}

// writeRequestHead 把处理过的请求头写给上游。
func writeRequestHead(w io.Writer, method, target, version string, header map[string][]string, order []string) error {
	var sb strings.Builder
	sb.WriteString(method)
	sb.WriteByte(' ')
	sb.WriteString(target)
	sb.WriteByte(' ')
	sb.WriteString(version)
	sb.WriteString("\r\n")
	for _, k := range order {
		for _, v := range header[k] {
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.WriteString(v)
			sb.WriteString("\r\n")
		}
	}
	sb.WriteString("\r\n")
	_, err := io.WriteString(w, sb.String())
	return err
}

// parseAbsoluteTarget 把绝对 URI（"http://host:port/path"）解析成 host、port、origin-form 路径。
func parseAbsoluteTarget(target string, defPort int) (host string, port int, path string, err error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", 0, "", fmt.Errorf("无法解析目标 URI %q: %w", target, err)
	}
	if u.Scheme != "http" {
		return "", 0, "", fmt.Errorf("不支持的 scheme %q（https 请使用 CONNECT）", u.Scheme)
	}
	if u.Host == "" {
		return "", 0, "", fmt.Errorf("目标 URI 缺少主机名: %q", target)
	}
	host = u.Hostname()
	port = defPort
	if ps := u.Port(); ps != "" {
		port, err = strconv.Atoi(ps)
		if err != nil {
			return "", 0, "", fmt.Errorf("端口 %q 不是数字", ps)
		}
	}
	p := u.RequestURI()
	if p == "" {
		p = "/"
	}
	return host, port, p, nil
}
