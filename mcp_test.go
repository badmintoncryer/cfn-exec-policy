package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tool calls map to the CLI's flags; notifications get no reply.
func TestMCP(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"exec_policy_explain","arguments":{"inputs":["cdk.out"],"strict":true,"scope_resources":true}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"exec_policy_diff","arguments":{"inputs":["head"]}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"exec_policy_doctor","arguments":{"stack":"App","region":"eu-west-1"}}}`,
	}, "\n")
	var calls []string
	var out bytes.Buffer
	if err := runMCP(strings.NewReader(in), &out, func(args []string) (string, bool) {
		calls = append(calls, strings.Join(args, " "))
		return "ok", false
	}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("want 5 responses, got %d:\n%s", len(lines), out.String())
	}
	for _, name := range []string{"exec_policy_explain", "exec_policy_generate", "exec_policy_diff", "exec_policy_doctor"} {
		if !strings.Contains(lines[1], `"name":"`+name+`"`) {
			t.Errorf("tools/list lacks %s", name)
		}
	}
	want := []string{
		"generate --explain --format json --scope-resources --strict -- cdk.out",
		"doctor App --format json --region eu-west-1",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s", strings.Join(calls, "\n"))
	}
	var diff map[string]any
	json.Unmarshal([]byte(lines[3]), &diff)
	if r := diff["result"].(map[string]any); r["isError"] != true {
		t.Errorf("diff without base: %v", r)
	}
}

// The built binary answers a real tool call.
func TestMCPBinary(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cfn-exec-policy")
	if b, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	cmd := exec.Command(bin, "mcp")
	cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"exec_policy_generate","arguments":{"inputs":["testdata/template.yaml"]}}}` + "\n")
	cmd.Env = append(os.Environ(), "AWS_REGION=us-east-1")
	b, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `\"Version\": \"2012-10-17\"`) || strings.Contains(string(b), `"isError":true`) {
		t.Errorf("tool result:\n%s", b)
	}
}
