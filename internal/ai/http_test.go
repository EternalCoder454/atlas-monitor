package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		raw, addr, host, base string
	}{
		{"http://localhost:11434", "localhost:11434", "localhost:11434", ""},
		{"http://localhost:11434/", "localhost:11434", "localhost:11434", ""},
		{"localhost:11434", "localhost:11434", "localhost:11434", ""},
		{"http://box", "box:11434", "box:11434", ""},
		{"http://10.0.0.5:8080/ollama", "10.0.0.5:8080", "10.0.0.5:8080", "/ollama"},
		{"http://[::1]:11434", "[::1]:11434", "[::1]:11434", ""},
		{" http://localhost:11434 ", "localhost:11434", "localhost:11434", ""},
	}
	for _, tt := range tests {
		got, err := parseEndpoint(tt.raw)
		if err != nil {
			t.Errorf("parseEndpoint(%q): %v", tt.raw, err)
			continue
		}
		if got.addr != tt.addr || got.host != tt.host || got.base != tt.base {
			t.Errorf("parseEndpoint(%q) = %+v, want addr=%q host=%q base=%q",
				tt.raw, got, tt.addr, tt.host, tt.base)
		}
	}
}

func TestParseEndpointRejectsHTTPS(t *testing.T) {
	if _, err := parseEndpoint("https://example.com"); !errors.Is(err, errHTTPS) {
		t.Fatalf("parseEndpoint(https) error = %v, want errHTTPS", err)
	}
	c := New("https://example.com", "m")
	if _, _, err := c.Chat(context.Background(), nil, nil); !errors.Is(err, errHTTPS) {
		t.Fatalf("Chat over https error = %v, want errHTTPS", err)
	}
}

// TestChatStreamsChunked exercises the chunked-transfer decoder against a real
// server that flushes each NDJSON line separately, the way Ollama streams.
func TestChatStreamsChunked(t *testing.T) {
	lines := []string{
		`{"message":{"role":"assistant","content":"Hello"},"done":false}`,
		`{"message":{"role":"assistant","content":", world"},"done":false}`,
		`{"message":{"role":"assistant","content":"!"},"done":false}`,
		`{"done":true,"eval_count":3,"eval_duration":1000000,"total_duration":2000000}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"stream":true`) {
			t.Errorf("request body missing stream flag: %s", body)
		}
		// No Content-Length and an explicit flush per line ⇒ chunked encoding.
		for _, l := range lines {
			io.WriteString(w, l+"\n")
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()

	var got []string
	full, stats, err := New(srv.URL, "test").Chat(context.Background(),
		[]Message{{Role: "user", Content: "hi"}},
		func(tok string) { got = append(got, tok) })
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if full != "Hello, world!" {
		t.Errorf("full = %q, want %q", full, "Hello, world!")
	}
	if len(got) != 3 {
		t.Errorf("streamed %d chunks, want 3 (%q)", len(got), got)
	}
	if stats.EvalCount != 3 {
		t.Errorf("EvalCount = %d, want 3", stats.EvalCount)
	}
}

// TestChatReportsServerError checks that a non-200 carries the server's message.
func TestChatReportsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":"model not found"}`)
	}))
	defer srv.Close()

	_, _, err := New(srv.URL, "missing").Chat(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("Chat error = %v, want it to mention the server's message", err)
	}
}

// TestPathPrefixAndHost checks that a base URL carrying a path prefix reaches
// the right endpoint and sends a matching Host header — the reverse-proxy case.
func TestPathPrefixAndHost(t *testing.T) {
	var gotPath, gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotHost = r.URL.Path, r.Host
		io.WriteString(w, `{"models":[{"name":"m:1"}]}`)
	}))
	defer srv.Close()

	if _, err := New(srv.URL+"/ollama", "m:1").Tags(context.Background()); err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if gotPath != "/ollama/api/tags" {
		t.Errorf("request path = %q, want /ollama/api/tags", gotPath)
	}
	if want := strings.TrimPrefix(srv.URL, "http://"); gotHost != want {
		t.Errorf("Host header = %q, want %q", gotHost, want)
	}
}

// TestTagsWithContentLength covers the non-chunked body path.
func TestTagsWithContentLength(t *testing.T) {
	const payload = `{"models":[{"name":"a:1"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		io.WriteString(w, payload)
	}))
	defer srv.Close()

	got, err := New(srv.URL, "a:1").Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if len(got) != 1 || got[0] != "a:1" {
		t.Fatalf("Tags = %v, want [a:1]", got)
	}
}
