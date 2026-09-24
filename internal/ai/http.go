package ai

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Atlas talks to Ollama over plain HTTP/1.1 on a socket it opens itself rather
// than through net/http. The API surface it needs is two endpoints on a local
// server, while net/http drags in crypto/tls, crypto/x509, the FIPS module and
// the HTTP/2 stack — around 2 MB of machine code and a 32 MiB static entropy
// buffer, none of which a localhost JSON call ever touches.
//
// The trade-off: https:// endpoints are not supported. Ollama serves plain HTTP
// (its own default is http://localhost:11434); reach a remote instance over an
// SSH tunnel or a reverse proxy that terminates TLS.

// defaultPort is assumed when the configured URL names no port. Ollama's
// standard port — this client only ever talks to Ollama.
const defaultPort = "11434"

// errHTTPS explains why a TLS endpoint cannot be used.
var errHTTPS = errors.New("https endpoints aren't supported — point Atlas at Ollama over http:// " +
	"(use an SSH tunnel for a remote server)")

// endpoint is a parsed Ollama base URL.
type endpoint struct {
	addr string // host:port for dialling
	host string // value for the Host header
	base string // path prefix, "" or "/something"
}

// parseEndpoint splits an Ollama base URL such as "http://localhost:11434" into
// its dial address and path prefix.
func parseEndpoint(raw string) (endpoint, error) {
	var e endpoint
	s := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(s, "https://"):
		return e, errHTTPS
	case strings.HasPrefix(s, "http://"):
		s = s[len("http://"):]
	}
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return e, fmt.Errorf("no Ollama URL configured")
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		e.base, s = s[i:], s[:i]
	}
	e.host = s
	e.addr = s
	if !hasPort(s) {
		e.addr = net.JoinHostPort(s, defaultPort)
		e.host = e.addr
	}
	return e, nil
}

// hasPort reports whether hostport already carries a ":port", accounting for
// bracketed IPv6 literals.
func hasPort(s string) bool {
	if i := strings.LastIndexByte(s, ']'); i >= 0 {
		return strings.IndexByte(s[i:], ':') >= 0
	}
	return strings.IndexByte(s, ':') >= 0
}

// response is a live HTTP response: its body plus the connection to close.
type response struct {
	status int
	body   io.Reader
	conn   net.Conn
	cancel func()
}

// Close releases the connection and the context watchdog.
func (r *response) Close() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.conn != nil {
		_ = r.conn.Close()
	}
}

// do performs one request and returns the response with its body ready to read.
// The caller must Close the result. The connection is closed when ctx is done,
// which unblocks a read in progress — that is what makes a streamed chat
// cancellable.
func (c *Client) do(ctx context.Context, method, path string, body []byte) (*response, error) {
	ep, err := parseEndpoint(c.url)
	if err != nil {
		return nil, err
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", ep.addr)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Ollama at %s: %w", c.url, err)
	}

	watch, cancel := context.WithCancel(ctx)
	go func() {
		<-watch.Done()
		if watch.Err() == context.DeadlineExceeded || ctx.Err() != nil {
			_ = conn.Close()
		}
	}()
	resp := &response{conn: conn, cancel: cancel}

	var req strings.Builder
	req.Grow(160 + len(path) + len(ep.base))
	req.WriteString(method)
	req.WriteByte(' ')
	req.WriteString(ep.base)
	req.WriteString(path)
	req.WriteString(" HTTP/1.1\r\nHost: ")
	req.WriteString(ep.host)
	req.WriteString("\r\nUser-Agent: atlas-monitor\r\nAccept: application/x-ndjson, application/json\r\nConnection: close\r\n")
	if body != nil {
		req.WriteString("Content-Type: application/json\r\nContent-Length: ")
		req.WriteString(strconv.Itoa(len(body)))
		req.WriteString("\r\n")
	}
	req.WriteString("\r\n")

	if _, err := io.WriteString(conn, req.String()); err != nil {
		resp.Close()
		return nil, fmt.Errorf("cannot reach Ollama at %s: %w", c.url, err)
	}
	if body != nil {
		if _, err := conn.Write(body); err != nil {
			resp.Close()
			return nil, fmt.Errorf("cannot reach Ollama at %s: %w", c.url, err)
		}
	}

	br := bufio.NewReaderSize(conn, headerBuf)
	status, chunked, length, err := readHead(br)
	if err != nil {
		resp.Close()
		return nil, fmt.Errorf("cannot reach Ollama at %s: %w", c.url, err)
	}
	resp.status = status
	switch {
	case chunked:
		resp.body = &chunkedReader{r: br}
	case length >= 0:
		resp.body = io.LimitReader(br, length)
	default:
		resp.body = br // server closes to signal the end
	}
	return resp, nil
}

// headerBuf is the bufio size used for the response. It also bounds a single
// header line: ReadSlice refuses a longer one rather than growing without
// limit, so a confused server cannot make the client allocate unboundedly.
const headerBuf = 8 * 1024

// readHead consumes the status line and headers, reporting the status code and
// how the body is framed (length -1 means "until the connection closes").
func readHead(br *bufio.Reader) (status int, chunked bool, length int64, err error) {
	line, err := readLine(br)
	if err != nil {
		return 0, false, 0, err
	}
	// "HTTP/1.1 200 OK"
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
		return 0, false, 0, fmt.Errorf("malformed response %q", line)
	}
	status, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, false, 0, fmt.Errorf("malformed status %q", parts[1])
	}

	length = -1
	for {
		line, err = readLine(br)
		if err != nil {
			return 0, false, 0, err
		}
		if line == "" {
			return status, chunked, length, nil
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch {
		case strings.EqualFold(name, "Transfer-Encoding"):
			chunked = strings.Contains(strings.ToLower(value), "chunked")
		case strings.EqualFold(name, "Content-Length"):
			if n, err := strconv.ParseInt(value, 10, 64); err == nil {
				length = n
			}
		}
	}
}

// readLine reads one CRLF-terminated header line. ReadSlice returns a view into
// the bufio buffer, which bounds the line length for free — a line that does not
// fit is an error rather than an unbounded allocation.
func readLine(br *bufio.Reader) (string, error) {
	line, err := br.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		return "", errors.New("response header line too long")
	}
	if err != nil {
		return "", err
	}
	return string(bytes.TrimRight(line, "\r\n")), nil
}

// chunkedReader decodes HTTP/1.1 chunked transfer encoding, which is how Ollama
// frames a streaming chat response.
type chunkedReader struct {
	r    *bufio.Reader
	left int64 // bytes remaining in the current chunk
	done bool
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if c.done {
		return 0, io.EOF
	}
	if c.left == 0 {
		if err := c.nextChunk(); err != nil {
			return 0, err
		}
		if c.done {
			return 0, io.EOF
		}
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// nextChunk consumes the CRLF after the previous chunk (if any) and the size
// line of the next one.
func (c *chunkedReader) nextChunk() error {
	line, err := readLine(c.r)
	if err != nil {
		return err
	}
	if line == "" { // trailing CRLF of the previous chunk
		if line, err = readLine(c.r); err != nil {
			return err
		}
	}
	if i := strings.IndexByte(line, ';'); i >= 0 { // chunk extensions
		line = line[:i]
	}
	size, err := strconv.ParseInt(strings.TrimSpace(line), 16, 64)
	if err != nil || size < 0 {
		return fmt.Errorf("malformed chunk size %q", line)
	}
	if size == 0 {
		c.done = true
		return nil
	}
	c.left = size
	return nil
}

// timeoutContext derives a context carrying the client's overall deadline.
func timeoutContext(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

// IsLocal reports whether the configured endpoint is on this machine.
//
// It matters because the assistant sends a live system snapshot — hostname,
// username, the running processes, the enabled services — as the system prompt,
// and this client speaks plaintext HTTP only: parseEndpoint rejects https
// outright. Pointed at localhost, which is what Atlas ships with and is built
// for, none of that leaves the machine. Pointed anywhere else, all of it crosses
// the network in the clear, and the person deserves to be told.
//
// Anything that is not recognisably a loopback address counts as remote. No name
// is resolved: this is called from the Settings dialog on the UI thread, and a
// DNS lookup there could block the interface.
func IsLocal(raw string) bool {
	ep, err := parseEndpoint(raw)
	if err != nil {
		return true // unusable anyway; nothing will be sent
	}
	host := ep.host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
