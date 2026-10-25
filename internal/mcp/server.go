// Package mcp serves lnch tools over the Model Context Protocol on stdio.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"lnch.sh/internal/run"
	"lnch.sh/internal/version"
)

// Serve reads MCP messages from r and writes responses to w until EOF.
func Serve(ctx context.Context, r io.Reader, w io.Writer, root string) error {
	s := &server{ctx: ctx, w: w, root: root, tools: toolset()}
	br := bufio.NewReader(r)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg, err := readMessage(br)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := s.handle(msg); err != nil {
			return err
		}
	}
}

type server struct {
	ctx   context.Context
	w     io.Writer
	root  string
	tools []tool
}

type tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Call        func(ctx context.Context, root string, args map[string]any) (any, error)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (s *server) handle(raw []byte) error {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		return s.reply(nil, nil, &rpcError{Code: -32700, Message: "parse error"})
	}
	if req.JSONRPC == "" {
		req.JSONRPC = "2.0"
	}
	id := req.ID
	if !hasID(id) {
		_, _ = s.dispatch(req)
		return nil
	}
	result, rpcErr := s.dispatch(req)
	return s.reply(id, result, rpcErr)
}

func (s *server) dispatch(req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "lnch", "version": version.Version},
		}, nil
	case "notifications/initialized", "initialized":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		out := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			out = append(out, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.Schema,
			})
		}
		return map[string]any{"tools": out}, nil
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return nil, &rpcError{Code: -32602, Message: err.Error()}
			}
		}
		for _, t := range s.tools {
			if t.Name != params.Name {
				continue
			}
			ctx, cancel := context.WithTimeout(s.ctx, 45*time.Second)
			defer cancel()
			value, err := t.Call(ctx, s.root, params.Arguments)
			if err != nil {
				return map[string]any{
					"content": []map[string]any{{"type": "text", "text": err.Error()}},
					"isError": true,
				}, nil
			}
			text := fmt.Sprint(value)
			if b, err := json.MarshalIndent(value, "", "  "); err == nil {
				text = string(b)
			}
			return map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": false,
			}, nil
		}
		return nil, &rpcError{Code: -32602, Message: "unknown tool " + params.Name}
	default:
		if strings.HasPrefix(req.Method, "notifications/") {
			return nil, nil
		}
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *server) reply(id json.RawMessage, result any, rpcErr *rpcError) error {
	resp := map[string]any{"jsonrpc": "2.0"}
	if hasID(id) {
		resp["id"] = json.RawMessage(id)
	}
	if rpcErr != nil {
		resp["error"] = rpcErr
	} else {
		resp["result"] = result
	}
	return writeMessage(s.w, resp)
}

func hasID(id json.RawMessage) bool {
	return len(id) > 0 && string(id) != "null"
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	b, err := r.Peek(1)
	if err != nil {
		return nil, err
	}
	if b[0] == '{' {
		return r.ReadBytes('\n')
	}
	var length int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			fmt.Sscanf(strings.TrimSpace(line[len("content-length:"):]), "%d", &length)
		}
	}
	if length <= 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	buf := make([]byte, length)
	_, err = io.ReadFull(r, buf)
	return buf, err
}

func writeMessage(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(b), b)
	return err
}

func toolset() []tool {
	obj := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}
	rootProp := map[string]any{"type": "string", "description": "Project directory. Defaults to the server working directory."}
	return []tool{
		{
			Name:        "lnch_detect",
			Description: "Detect the dev services lnch would start in a project. Does not launch anything.",
			Schema:      obj(map[string]any{"root": rootProp}),
			Call: func(ctx context.Context, root string, args map[string]any) (any, error) {
				p, err := run.LoadPlan(argRoot(args, root))
				if err != nil {
					return nil, err
				}
				return p, nil
			},
		},
		{
			Name:        "lnch_up",
			Description: "Detect and launch the dev stack in the background. Idempotent when the stack is already running.",
			Schema: obj(map[string]any{
				"root":    rootProp,
				"share":   map[string]any{"type": "boolean", "description": "Also publish a LAN or relay URL."},
				"dry_run": map[string]any{"type": "boolean", "description": "Return the plan without starting processes."},
				"relay":   map[string]any{"type": "string", "description": "Tunnel relay base URL. Defaults to https://lnch.sh. Use off for LAN only."},
			}),
			Call: func(ctx context.Context, root string, args map[string]any) (any, error) {
				res, err := run.Launch(ctx, run.Options{
					Root:   argRoot(args, root),
					JSON:   true,
					DryRun: argBool(args, "dry_run"),
					Detach: true,
					NoTUI:  true,
					Share:  argBool(args, "share"),
					Relay:  argString(args, "relay"),
					Wait:   20 * time.Second,
					Stdout: io.Discard,
					Stderr: io.Discard,
				})
				if err != nil {
					return nil, err
				}
				return res, nil
			},
		},
		{
			Name:        "lnch_down",
			Description: "Stop the dev stack for a project. Succeeds when it is already stopped.",
			Schema:      obj(map[string]any{"root": rootProp}),
			Call: func(ctx context.Context, root string, args map[string]any) (any, error) {
				msg, err := run.Down(ctx, argRoot(args, root))
				if err != nil {
					return nil, err
				}
				return map[string]string{"status": msg}, nil
			},
		},
		{
			Name:        "lnch_status",
			Description: "Return ports, health, and URLs for the running stack.",
			Schema:      obj(map[string]any{"root": rootProp}),
			Call: func(ctx context.Context, root string, args map[string]any) (any, error) {
				return run.Status(ctx, argRoot(args, root))
			},
		},
		{
			Name:        "lnch_logs",
			Description: "Return recent logs for one service, or all services when name is omitted.",
			Schema: obj(map[string]any{
				"root":    rootProp,
				"service": map[string]any{"type": "string"},
				"tail":    map[string]any{"type": "integer", "description": "Number of lines. Default 100."},
			}),
			Call: func(ctx context.Context, root string, args map[string]any) (any, error) {
				return run.LogSnapshot(ctx, argRoot(args, root), argString(args, "service"), argInt(args, "tail", 100))
			},
		},
		{
			Name:        "lnch_restart",
			Description: "Restart one service in the running stack.",
			Schema: obj(map[string]any{
				"root":    rootProp,
				"service": map[string]any{"type": "string", "description": "Service name, for example web or api."},
			}),
			Call: func(ctx context.Context, root string, args map[string]any) (any, error) {
				name := argString(args, "service")
				if name == "" {
					return nil, fmt.Errorf("service is required\nExample: lnch restart web")
				}
				return run.Restart(ctx, argRoot(args, root), name)
			},
		},
		{
			Name:        "lnch_share",
			Description: "Publish the running stack. The public URL uses the relay from launch: lnch_up relay, LNCH_RELAY, share.relay, or https://lnch.sh.",
			Schema:      obj(map[string]any{"root": rootProp}),
			Call: func(ctx context.Context, root string, args map[string]any) (any, error) {
				return run.Share(ctx, argRoot(args, root))
			},
		},
	}
}

func argRoot(args map[string]any, fallback string) string {
	if v := argString(args, "root"); v != "" {
		return v
	}
	return fallback
}

func argString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, _ := args[key].(string)
	return v
}

func argBool(args map[string]any, key string) bool {
	if args == nil {
		return false
	}
	v, _ := args[key].(bool)
	return v
}

func argInt(args map[string]any, key string, fallback int) int {
	if args == nil {
		return fallback
	}
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return fallback
	}
}
