package ui

import "testing"

func TestModelInstalled(t *testing.T) {
	models := []string{"qwen2.5:3b", "llama3.2:latest", "nomic-embed-text:latest"}
	cases := []struct {
		want string
		ok   bool
	}{
		{"qwen2.5:3b", true},
		{"qwen2.5", true},         // untagged matches the :3b tag
		{"llama3.2", true},        // untagged matches the :latest tag
		{"llama3.2:latest", true},
		{"qwen2.5:7b", false},     // a specific tag that is not present
		{"mistral", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := modelInstalled(models, tc.want); got != tc.ok {
			t.Errorf("modelInstalled(models, %q) = %v, want %v", tc.want, got, tc.ok)
		}
	}
}
