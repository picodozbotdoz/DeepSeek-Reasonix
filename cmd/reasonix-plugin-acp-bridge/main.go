// Command reasonix-plugin-acp-bridge is an MCP server that wraps `reasonix acp`,
// exposing a `delegate_task` tool. When the manager calls it, the bridge:
//
//  1. spawns `reasonix acp` in the learner workspace
//  2. opens an ACP session
//  3. sends a session/prompt with the task
//  4. collects the agent's streaming output (thoughts, messages, tool results)
//  5. auto-approves permission requests so the learner can use tools
//  6. closes the session and returns the consolidated result
//
// Wire it up in reasonix.toml:
//
//	[[plugins]]
//	name    = "learner"
//	command = "reasonix-plugin-acp-bridge"
//	args    = ["-learner-dir", "../learner"]
//
// Then the manager can call mcp__learner__delegate_task(task="...") to delegate
// work to the learner.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var version = "dev"

func main() {
	log.SetPrefix("acp-bridge: ")
	log.SetFlags(log.Ltime | log.Lshortfile)

	learnerDir := "."
	for i, arg := range os.Args[1:] {
		if arg == "-learner-dir" && i+1 < len(os.Args[1:]) {
			learnerDir = os.Args[2+i]
		}
	}
	absDir, err := filepath.Abs(learnerDir)
	if err != nil {
		log.Fatalf("resolving -learner-dir: %v", err)
	}
	log.Printf("learner dir: %s", absDir)

	if err := serve(os.Stdin, os.Stdout, absDir); err != nil {
		log.Fatal(err)
	}
}

// ─── MCP JSON-RPC framing ───────────────────────────────────────────────

type request struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	mcpProtocolVersion = "2024-11-05"
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// ─── MCP server loop ────────────────────────────────────────────────────

func serve(in *os.File, out *os.File, learnerDir string) error {
	r := bufio.NewReader(in)
	w := bufio.NewWriter(out)
	defer w.Flush()

	bridge := &acpBridge{learnerDir: learnerDir}

	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if rerr := bridge.handleLine(line, w); rerr != nil {
				return rerr
			}
			if ferr := w.Flush(); ferr != nil {
				return ferr
			}
		}
		if err != nil {
			return nil
		}
	}
}

type acpBridge struct {
	learnerDir string
}

func (b *acpBridge) handleLine(line []byte, w *bufio.Writer) error {
	line = trimSpace(line)
	if len(line) == 0 {
		return nil
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		log.Printf("skipping unparseable line: %v", err)
		return nil
	}
	if req.ID == nil {
		return nil
	}

	resp := response{JSONRPC: "2.0", ID: *req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "reasonix-plugin-acp-bridge", "version": version},
		}
	case "tools/list":
		resp.Result = map[string]any{"tools": toolList()}
	case "tools/call":
		resp.Result, resp.Error = b.callTool(req.Params)
	default:
		resp.Error = &rpcError{Code: codeMethodNotFound, Message: "method not found: " + req.Method}
	}

	respRaw, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	if _, err := w.Write(append(respRaw, '\n')); err != nil {
		return err
	}
	return nil
}

// ─── Tool definitions ───────────────────────────────────────────────────

func toolList() []map[string]any {
	return []map[string]any{
		{
			"name":        "delegate_task",
			"description": "Delegate a task to the learner Reasonix agent. The learner runs autonomously in its own workspace, thinks through the problem, calls tools (read_file, bash, edit_file, etc.), and returns the result. Use this when a task belongs to the learner's workspace or you want a focused sub-agent to handle it.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{
						"type":        "string",
						"description": "The task description for the learner agent. Be specific and include all context the learner needs.",
					},
					"cwd": map[string]any{
						"type":        "string",
						"description": "Working directory for the learner session (absolute path). Defaults to learner dir.",
					},
				},
				"required": []string{"task"},
			},
			"annotations": map[string]any{
				"readOnlyHint": false,
				"title":        "Delegate task to learner",
			},
		},
	}
}

func (b *acpBridge) callTool(params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	if p.Name != "delegate_task" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "unknown tool: " + p.Name}
	}

	task, _ := p.Arguments["task"].(string)
	cwd, _ := p.Arguments["cwd"].(string)

	if task == "" {
		return textResult("argument 'task' is required and must be a non-empty string", true), nil
	}

	log.Printf("delegate_task called, task length=%d, cwd=%s", len(task), strOr(cwd, b.learnerDir))

	result, err := b.delegateTask(task, cwd)
	if err != nil {
		return textResult(fmt.Sprintf("error delegating task: %v", err), true), nil
	}
	return textResult(result, false), nil
}

func textResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

// ─── ACP client ─────────────────────────────────────────────────────────

type acpClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID int
}

func startACP(learnerDir string) (*acpClient, error) {
	reasonix := "/home/doz/bin/reasonix-worker"
	if _, err := os.Stat(reasonix); err != nil {
		if p, err := exec.LookPath("reasonix"); err == nil {
			reasonix = p
		} else {
			reasonix = "reasonix"
		}
	}

	cmd := exec.Command(reasonix, "acp")
	cmd.Dir = learnerDir

	// Point XDG_CONFIG_HOME to a writable directory inside the worker workspace
	// so ACP session transcripts can be persisted to disk and resumed across
	// calls. The default (~/.config/reasonix/) is read-only inside the sandbox.
	rxHome := filepath.Join(learnerDir, ".reasonix-home")
	os.MkdirAll(rxHome, 0755)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+rxHome)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start reasonix acp: %w", err)
	}

	return &acpClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
		nextID: 1,
	}, nil
}

func (c *acpClient) close() {
	c.stdin.Close()
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	c.cmd.Wait()
}

// sendACPRequest writes a JSON-RPC request and returns the assigned id.
func (c *acpClient) sendACPRequest(method string, params any) (int, error) {
	id := c.nextID
	c.nextID++

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return 0, fmt.Errorf("marshal request: %w", err)
	}
	if _, err := fmt.Fprintf(c.stdin, "%s\n", reqJSON); err != nil {
		return 0, fmt.Errorf("write request: %w", err)
	}
	return id, nil
}

// readACPResponse reads from the ACP subprocess until it finds the response
// matching the given requestID. Along the way, it handles:
//   - notifications (no id) → collected
//   - incoming requests with an id (e.g. session/request_permission) → auto-handled
//   - the final response matching requestID → returned
func (c *acpClient) readACPResponse(requestID int) (json.RawMessage, []json.RawMessage, error) {
	var notifications []json.RawMessage

	for {
		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			return nil, nil, fmt.Errorf("read: %w", err)
		}
		line = trimSpace(line)
		if len(line) == 0 {
			continue
		}

		// Log raw line for debugging (truncated)
		lineStr := string(line)
		if len(lineStr) > 200 {
			lineStr = lineStr[:200] + "..."
		}
		log.Printf("ACP << %s", lineStr)

		var raw struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method,omitempty"`
		}
		if err := json.Unmarshal(line, &raw); err != nil {
			log.Printf("skipping unparseable ACP line: %v", err)
			continue
		}

		if raw.ID == nil {
			// Notification — collect it
			log.Printf("ACP notification: method=%s", raw.Method)
			notifications = append(notifications, line)
			continue
		}

		// Has an id — determine whether it's a response or incoming request
		// ACP server uses int64 IDs (1, 2, 3…), but also accept string IDs
		var incomingID int
		if err := json.Unmarshal(*raw.ID, &incomingID); err != nil {
			// Try string ID as fallback
			var idStr string
			if err2 := json.Unmarshal(*raw.ID, &idStr); err2 != nil {
				log.Printf("ACP line with unparsable ID, skipping: method=%s", raw.Method)
				continue
			}
			log.Printf("ACP string ID=%s (expected int), method=%s", idStr, raw.Method)
			// Can't match int requestID — auto-respond and collect
			if raw.Method != "" {
				notifications = append(notifications, line)
				c.autoRespond(raw.Method, line, 0)
			}
			continue
		}

		if incomingID == requestID {
			// The response to our original request
			log.Printf("ACP response received for id=%d", requestID)
			var rpcResp struct {
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(line, &rpcResp); err != nil {
				return nil, nil, fmt.Errorf("parse response: %w", err)
			}
			if rpcResp.Error != nil {
				return nil, nil, fmt.Errorf("ACP error (code %d): %s", rpcResp.Error.Code, rpcResp.Error.Message)
			}
			return rpcResp.Result, notifications, nil
		}

		// Incoming request (with a different id) — e.g. session/request_permission
		log.Printf("ACP incoming request: method=%s id=%d (expecting %d)", raw.Method, incomingID, requestID)
		notifications = append(notifications, line)
		c.autoRespond(raw.Method, line, incomingID)
	}
}

// autoRespond handles incoming ACP requests from the server (like
// session/request_permission) by auto-approving them.
func (c *acpClient) autoRespond(method string, rawLine json.RawMessage, requestID int) {
	switch method {
	case "session/request_permission":
		// Parse the permission request to know what tool is being called
		var permReq struct {
			Params struct {
				ToolCall struct {
					Title string `json:"title"`
					Kind  string `json:"kind"`
				} `json:"toolCall"`
				Options []struct {
					OptionID string `json:"optionId"`
					Kind     string `json:"kind"`
				} `json:"options"`
			} `json:"params"`
		}
		if err := json.Unmarshal(rawLine, &permReq); err != nil {
			log.Printf("failed to parse permission request: %v", err)
			return
		}

		// Choose "allow_once" if available, otherwise the first option
		optionID := ""
		for _, opt := range permReq.Params.Options {
			if opt.Kind == "allow_once" {
				optionID = opt.OptionID
				break
			}
		}
		if optionID == "" && len(permReq.Params.Options) > 0 {
			optionID = permReq.Params.Options[0].OptionID
		}

		log.Printf("auto-approving: %s (%s) option=%s",
			permReq.Params.ToolCall.Title, permReq.Params.ToolCall.Kind, optionID)

		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      requestID,
			"result": map[string]any{
				"outcome": map[string]any{
					"outcome":  "selected",
					"optionId": optionID,
				},
			},
		}
		b, _ := json.Marshal(resp)
		fmt.Fprintf(c.stdin, "%s\n", b)

	default:
		log.Printf("unknown incoming request: %s (id=%d)", method, requestID)
		// Respond with method-not-found so the server doesn't hang
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      requestID,
			"error": map[string]any{
				"code":    codeMethodNotFound,
				"message": "method not found: " + method,
			},
		}
		b, _ := json.Marshal(resp)
		fmt.Fprintf(c.stdin, "%s\n", b)
	}
}

// acpCall is a convenience wrapper: send + read + collect notifications.
func (c *acpClient) acpCall(method string, params any) (json.RawMessage, []json.RawMessage, error) {
	id, err := c.sendACPRequest(method, params)
	if err != nil {
		return nil, nil, err
	}
	return c.readACPResponse(id)
}

// sessionState tracks a persistent ACP session that can be resumed across calls.
type sessionState struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
}

func sessionStatePath(learnerDir string) string {
	return filepath.Join(learnerDir, ".reasonix_session.json")
}

func loadSessionState(learnerDir string) (*sessionState, error) {
	path := sessionStatePath(learnerDir)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // first call, no state yet
		}
		return nil, err
	}
	var s sessionState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.SessionID == "" {
		return nil, nil
	}
	// Verify the transcript file still exists
	if s.TranscriptPath != "" {
		if _, err := os.Stat(s.TranscriptPath); err != nil {
			log.Printf("transcript file missing (%s), starting fresh", s.TranscriptPath)
			return nil, nil
		}
	}
	return &s, nil
}

func saveSessionState(learnerDir string, s *sessionState) error {
	if s == nil {
		os.Remove(sessionStatePath(learnerDir))
		return nil
	}
	path := sessionStatePath(learnerDir)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

// ─── delegateTask: the core logic ───────────────────────────────────────

func (b *acpBridge) delegateTask(task string, cwd string) (string, error) {
	if cwd == "" {
		cwd = b.learnerDir
	}

	client, err := startACP(b.learnerDir)
	if err != nil {
		return "", err
	}
	defer client.close()

	// 1. Initialize
	_, _, err = client.acpCall("initialize", map[string]any{
		"protocolVersion": 1,
		"clientInfo":      map[string]any{"name": "reasonix-manager", "version": version},
	})
	if err != nil {
		return "", fmt.Errorf("initialize: %w", err)
	}

	// 2. Try to resume previous session, or create a new one
	sessionID := ""
	transcriptPath := ""

	prevState, err := loadSessionState(b.learnerDir)
	if err != nil {
		log.Printf("warning: failed to load session state: %v", err)
	}
	if prevState != nil {
		// Try session/load — this replays the conversation history into the new
		// ACP process so the agent sees its past context.
		log.Printf("attempting to resume session %s", prevState.SessionID)
		_, _, err := client.acpCall("session/load", map[string]any{
			"sessionId": prevState.SessionID,
			"cwd":       cwd,
		})
		if err != nil {
			log.Printf("session/load failed (%v), creating new session", err)
			prevState = nil // fallback to new
		} else {
			sessionID = prevState.SessionID
			transcriptPath = prevState.TranscriptPath
			log.Printf("resumed session: %s", sessionID)
		}
	}

	if prevState == nil {
		resultRaw, _, err := client.acpCall("session/new", map[string]any{"cwd": cwd})
		if err != nil {
			return "", fmt.Errorf("session/new: %w", err)
		}
		var sessionResult struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(resultRaw, &sessionResult); err != nil {
			return "", fmt.Errorf("parse session/new result: %w", err)
		}
		sessionID = sessionResult.SessionID
		log.Printf("opened new session: %s (cwd=%s)", sessionID, cwd)
	}

	// 3. Session/prompt with a 5-minute timeout
	type promptResult struct {
		raw    json.RawMessage
		notifs []json.RawMessage
		err    error
	}
	promptCh := make(chan promptResult, 1)

	go func() {
		promptRaw, notifs, perr := client.acpCall("session/prompt", map[string]any{
			"sessionId": sessionID,
			"prompt":    []map[string]any{{"type": "text", "text": task}},
		})
		promptCh <- promptResult{promptRaw, notifs, perr}
	}()

	select {
	case res := <-promptCh:
		if res.err != nil {
			client.acpCall("session/close", map[string]any{"sessionId": sessionID})
			return "", fmt.Errorf("session/prompt: %w", res.err)
		}
		promptResultRaw := res.raw
		notifications := res.notifs

		var promptResult struct {
			StopReason     string  `json:"stopReason"`
			TranscriptPath *string `json:"transcriptPath,omitempty"`
		}
		json.Unmarshal(promptResultRaw, &promptResult)

		// Update transcript path from the response (it's set after the first save)
		if promptResult.TranscriptPath != nil && *promptResult.TranscriptPath != "" {
			transcriptPath = *promptResult.TranscriptPath
		}

		// 4. Close the session on the ACP side (transcript is already persisted)
		client.acpCall("session/close", map[string]any{"sessionId": sessionID})

		// 5. Save session state for resumption on the next call
		saveSessionState(b.learnerDir, &sessionState{
			SessionID:      sessionID,
			TranscriptPath: transcriptPath,
			Cwd:            cwd,
		})

		// 6. Format the result
		output := formatResult(notifications)
		log.Printf("session %s done: reason=%s, output=%d chars",
			sessionID, promptResult.StopReason, len(output))

		return output, nil

	case <-time.After(5 * time.Minute):
		log.Printf("session %s timed out after 5 minutes, killing", sessionID)
		client.close()
		return "", fmt.Errorf("delegate_task timed out after 5 minutes")
	}
}

// ─── Result formatting ──────────────────────────────────────────────────

// formatResult converts ACP notifications into readable text.
func formatResult(notifications []json.RawMessage) string {
	var parts []string

	for _, n := range notifications {
		// Determine if it's a notification or an incoming request
		var base struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(n, &base); err != nil {
			continue
		}

		switch base.Method {
		case "session/update":
			var up struct {
				Update json.RawMessage `json:"update"`
			}
			if err := json.Unmarshal(base.Params, &up); err != nil {
				continue
			}
			parts = append(parts, formatUpdate(up.Update))

		case "session/request_permission":
			var perm struct {
				Params struct {
					ToolCall struct {
						Title string `json:"title"`
					} `json:"toolCall"`
				} `json:"params"`
			}
			if json.Unmarshal(n, &perm) == nil && perm.Params.ToolCall.Title != "" {
				parts = append(parts, fmt.Sprintf("  [using %s … done]", perm.Params.ToolCall.Title))
			}
		}
	}

	// Clean up: remove consecutive duplicates, trim
	var cleaned []string
	for _, p := range parts {
		if len(cleaned) == 0 || cleaned[len(cleaned)-1] != p {
			cleaned = append(cleaned, p)
		}
	}

	output := strings.Join(cleaned, "")
	output = strings.TrimSpace(output)
	if output == "" {
		output = "(empty response — check learner logs on stderr for details)"
	}
	return output
}

func formatUpdate(raw json.RawMessage) string {
	var upd struct {
		SessionUpdate string          `json:"sessionUpdate"`
		Content       json.RawMessage `json:"content,omitempty"`
		Title         string          `json:"title,omitempty"`
		Status        string          `json:"status,omitempty"`
	}
	if err := json.Unmarshal(raw, &upd); err != nil {
		return ""
	}

	switch upd.SessionUpdate {
	case "agent_thought_chunk":
		var c struct{ Text string `json:"text"` }
		if upd.Content != nil {
			json.Unmarshal(upd.Content, &c)
		}
		if c.Text != "" {
			return c.Text
		}

	case "agent_message_chunk":
		var c struct{ Text string `json:"text"` }
		if upd.Content != nil {
			json.Unmarshal(upd.Content, &c)
		}
		return c.Text

	case "tool_call":
		return fmt.Sprintf("  [using %s … ", upd.Title)

	case "tool_call_update":
		if upd.Status == "completed" || upd.Status == "success" {
			return "done]\n"
		} else if upd.Status == "error" {
			return "error]\n"
		}
	}

	return ""
}

// ─── Helpers ─────────────────────────────────────────────────────────────

func strOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func trimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
