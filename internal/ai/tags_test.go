package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestTagsParsesModelNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"models":[{"name":"qwen2.5:3b","model":"qwen2.5:3b"},{"name":"llama3.2:latest"}]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "qwen2.5:3b")
	got, err := c.Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	want := []string{"qwen2.5:3b", "llama3.2:latest"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tags = %v, want %v", got, want)
	}
}

func TestTagsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now → connection refused

	c := New(url, "qwen2.5:3b")
	if _, err := c.Tags(context.Background()); err == nil {
		t.Fatal("expected an error when the server is unreachable")
	}
}
