package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// `cfn-exec-policy mcp` is a Model Context Protocol server on stdin/stdout, so an
// AI agent writing CDK can see what the execution role needs without parsing
// the CLI's text. Each tool call runs this same binary with the matching flags and
// returns what it printed.

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	args        func(in map[string]any) ([]string, error)
}

var mcpTools = []mcpTool{
	{
		Name: "exec_policy_explain",
		Description: "List the IAM actions the CloudFormation execution role needs to deploy a synthesized AWS CDK app " +
			"(or CloudFormation templates), and for each action the resources that need it and why: schema handler " +
			"permissions, a hand-written (measured) row, a <service>:* fallback for a type with no measured permissions, " +
			"a custom resource, a nested stack, an SSM parameter type or a dynamic reference. Offline, no AWS credentials. " +
			"Run `cdk synth` first.",
		InputSchema: mcpInputs(nil),
		args: func(in map[string]any) ([]string, error) {
			return append(append([]string{"generate", "--explain", "--format", "json"}, mcpFlags(in)...), mcpInputList(in)...), nil
		},
	},
	{
		Name: "exec_policy_generate",
		Description: "Generate the least-privilege IAM policy document(s) for the CloudFormation execution role " +
			"(the role `cdk bootstrap --cloudformation-execution-policies` attaches) from a synthesized AWS CDK app. " +
			"Returns the JSON policy, or a list when it is over IAM's size limit. Offline, no AWS credentials.",
		InputSchema: mcpInputs(nil),
		args: func(in map[string]any) ([]string, error) {
			return append(append([]string{"generate"}, mcpFlags(in)...), mcpInputList(in)...), nil
		},
	},
	{
		Name: "exec_policy_diff",
		Description: "Show which permissions of the CloudFormation execution role a change adds or drops, compared " +
			"with a base cdk.out (e.g. one synthesized from the main branch), as Markdown. Additions worth a closer look " +
			"(<service>:* wildcards, iam:PassRole without a service condition, unknown types) are marked with !. Offline.",
		InputSchema: mcpInputs(map[string]any{"base": map[string]any{"type": "string", "description": "cdk.out or template to compare against"}}, "base"),
		args: func(in map[string]any) ([]string, error) {
			base, _ := in["base"].(string)
			if base == "" {
				return nil, fmt.Errorf("base is required")
			}
			return append(append([]string{"diff", "--format", "md", "--base", base}, mcpFlags(in)...), mcpInputList(in)...), nil
		},
	},
	{
		Name: "exec_policy_doctor",
		Description: "After a failed `cdk deploy`, read the stack's last operation in CloudFormation and list the resources " +
			"that failed because the execution role was denied an action, with the action, and whether the deployed " +
			"cfn-exec-policy policy lacks it or something else (a permissions boundary, an SCP) denied it. " +
			"Needs AWS credentials in the server's environment (read-only).",
		InputSchema: map[string]any{"type": "object", "required": []string{"stack"}, "properties": map[string]any{
			"stack":  map[string]any{"type": "string", "description": "stack name or ARN"},
			"region": map[string]any{"type": "string", "description": "region of the stack (default: the configured one)"},
		}},
		args: func(in map[string]any) ([]string, error) {
			stack, _ := in["stack"].(string)
			if stack == "" {
				return nil, fmt.Errorf("stack is required")
			}
			a := []string{"doctor", stack, "--format", "json"}
			if r, ok := in["region"].(string); ok && r != "" {
				a = append(a, "--region", r)
			}
			return a, nil
		},
	},
}

// mcpInputs is the input schema of the tools that read templates, plus extra properties.
func mcpInputs(extra map[string]any, required ...string) map[string]any {
	props := map[string]any{
		"inputs":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "cdk.out directories or template files (default ./cdk.out)"},
		"strict":              map[string]any{"type": "boolean", "description": "the --strict policy: a permissions boundary on every role and iam:PassRole scoped to the stacks' roles"},
		"pass_role_condition": map[string]any{"type": "boolean", "description": "limit iam:PassRole to the services the templates pass roles to"},
		"scope_resources":     map[string]any{"type": "boolean", "description": "limit SQS and SNS actions to the stacks' own queues and topics (experimental)"},
	}
	for k, v := range extra {
		props[k] = v
	}
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func mcpFlags(in map[string]any) []string {
	var a []string
	for k, flag := range map[string]string{"strict": "--strict", "pass_role_condition": "--pass-role-condition", "scope_resources": "--scope-resources"} {
		if b, ok := in[k].(bool); ok && b {
			a = append(a, flag)
		}
	}
	sort.Strings(a)
	return a
}

// mcpInputList returns the inputs, after a "--" so that no input reads as a flag.
func mcpInputList(in map[string]any) []string {
	return append([]string{"--"}, mcpStrings(in["inputs"])...)
}

func mcpStrings(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// runMCP serves MCP requests, one JSON-RPC message per line, until in closes.
// call runs one tool invocation (the binary itself, in production).
func runMCP(in io.Reader, out io.Writer, call func(args []string) (string, bool)) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal(line, &req); err != nil {
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error"}})
			continue
		}
		if len(req.ID) == 0 { // a notification (notifications/initialized, cancelled): no reply
			continue
		}
		result, rpcErr := mcpHandle(req, call)
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			resp["error"] = rpcErr
		} else {
			resp["result"] = result
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func mcpHandle(req mcpRequest, call func(args []string) (string, bool)) (any, map[string]any) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		v := p.ProtocolVersion
		if v == "" {
			v = "2025-06-18"
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "cfn-exec-policy", "version": version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, map[string]any{"code": -32602, "message": "invalid params"}
		}
		for _, t := range mcpTools {
			if t.Name != p.Name {
				continue
			}
			args, err := t.args(p.Arguments)
			if err != nil {
				return mcpText(err.Error(), true), nil
			}
			text, failed := call(args)
			return mcpText(text, failed), nil
		}
		return nil, map[string]any{"code": -32602, "message": "unknown tool " + p.Name}
	}
	return nil, map[string]any{"code": -32601, "message": "method not found: " + req.Method}
}

func mcpText(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}
}

// mcpExec runs this binary with args. Exit code 3 (diff --fail-on, doctor finding
// permission errors) is a result, not an error.
func mcpExec(args []string) (string, bool) {
	self, err := os.Executable()
	if err != nil {
		return err.Error(), true
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(self, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	text := stdout.String()
	if s := strings.TrimSpace(stderr.String()); s != "" {
		text += "\n" + s
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 3 {
		return text, false
	}
	return text, err != nil
}
