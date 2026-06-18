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
//
// # Scaling to many workers
//
// Each bridge process serves MCP `tools/call` synchronously — it handles one
// `delegate_task` at a time. Pooling ACP subprocesses inside one bridge (via
// -pool-max-total) only reuses a warm process for the *next* call; it does NOT
// make concurrent calls within the same bridge. To delegate to multiple workers
// simultaneously, declare one [[plugins]] entry per worker:
//
//	[[plugins]]
//	name    = "learner-01"
//	command = "reasonix-plugin-acp-bridge"
//	args    = ["-learner-dir", "../workers/learner-01", "-pool-max-total", "1"]
//
//	[[plugins]]
//	name    = "learner-02"
//	command = "reasonix-plugin-acp-bridge"
//	args    = ["-learner-dir", "../workers/learner-02", "-pool-max-total", "1"]
//
//	[[plugins]]
//	name    = "coder-01"
//	command = "reasonix-plugin-acp-bridge"
//	args    = ["-learner-dir", "../workers/coder-01", "-pool-max-total", "1"]
//
// Each bridge is its own OS process with its own stdio MCP connection.
// The manager agent's cross-server parallel dispatch treats
// mcp__learner-01__*, mcp__learner-02__*, and mcp__coder-01__* as distinct
// servers and runs calls to them concurrently (capped at 8 by default).
//
// Memory overhead: each bridge process (~10 MB RSS) plus one `reasonix acp`
// subprocess (~20-50 MB RSS when active). With 50 workers you can expect
// ~500 MB-3 GB of peak RSS. Use -pool-max-total 1 (the default) so each
// bridge spawns its ACP subprocess on demand and reuses it, keeping the active
// count bounded by the number of concurrent delegations.
//
// The -mono-task flag spawns a fresh `reasonix acp` per call and kills it on
// completion, avoiding the pool entirely. This is useful when ACP processes
// accumulate state that must be isolated between calls.
//
// # Timeouts for long-running tasks
//
// The bridge hard-caps each delegate_task at 5 minutes by default. Use
// -task-timeout to raise (or lower) this limit:
//
//	args = ["-learner-dir", "../worker", "-task-timeout", "30m"]
//
// A worker spawned with `reasonix acp` runs the full Reasonix agent loop. The
// ACP subprocess loads its config from <learnerDir>/reasonix.toml automatically
// (the same config resolution as a normal Reasonix session), so bash timeouts
// and other settings apply without any bridge-level changes. Its
// [tools] bash_timeout_seconds (default 120s) bounds individual bash calls.
// Set bash_timeout_seconds = 0 in the worker's reasonix.toml to disable the
// tool-local cap, or use bash(run_in_background=true) + wait() for commands
// that must outlive the foreground timeout.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

var version = "dev"

const defaultTaskTimeout = 5 * time.Minute

func main() {
	log.SetPrefix("acp-bridge: ")
	log.SetFlags(log.Ltime | log.Lshortfile)

	learnerDir := "."
	monoTaskACP := false
	poolMaxIdle := defaultPoolMaxIdle
	poolMaxTotal := defaultPoolMaxTotal
	taskTimeout := defaultTaskTimeout

	args := os.Args[1:]
	for i, arg := range args {
		switch arg {
		case "-mono-task":
			monoTaskACP = true
		case "-pool-max-idle":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &poolMaxIdle)
			}
		case "-pool-max-total":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &poolMaxTotal)
			}
		case "-learner-dir":
			if i+1 < len(args) {
				learnerDir = args[i+1]
			}
		case "-task-timeout":
			if i+1 < len(args) {
				d, err := time.ParseDuration(args[i+1])
				if err == nil && d > 0 {
					taskTimeout = d
				}
			}
		}
	}
	absDir, err := filepath.Abs(learnerDir)
	if err != nil {
		log.Fatalf("resolving -learner-dir: %v", err)
	}
	log.Printf("learner dir: %s, monoTaskACP: %v, pool: idle=%d total=%d, taskTimeout: %s", absDir, monoTaskACP, poolMaxIdle, poolMaxTotal, taskTimeout)

	wc := loadWorkerConfig(absDir)
	if wc.Description != "" {
		log.Printf("worker description: %q", wc.Description)
	}

	if err := serve(os.Stdin, os.Stdout, absDir, monoTaskACP, poolMaxIdle, poolMaxTotal, taskTimeout, wc.Description); err != nil {
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

func serve(in *os.File, out *os.File, learnerDir string, monoTaskACP bool, maxIdle, maxTotal int, taskTimeout time.Duration, descOverride string) error {
	r := bufio.NewReader(in)
	w := bufio.NewWriter(out)
	defer w.Flush()

	var pool *acpPool
	if !monoTaskACP {
		pool = newACPPool(learnerDir, maxIdle, maxTotal)
		defer pool.drain()
	}

	bridge := &acpBridge{
		learnerDir:        learnerDir,
		pool:              pool,
		monoTaskACP:       monoTaskACP,
		taskTimeout:       taskTimeout,
		descOverride:      descOverride,
		workers:           make(map[string]*asyncWorker),
		heartbeatInterval: 5 * time.Minute,
		deadTimeout:       10 * time.Minute,
	}

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

// workerConfig is an optional per-worker descriptor loaded from
// <learnerDir>/worker.toml. When present, its fields tailor the bridge's
// tool definitions so the manager agent can distinguish workers by role.
type workerConfig struct {
	// Description overrides the generic delegate_task description. Use it to
	// tell the manager what this worker specialises in.
	Description string `toml:"description"`
}

// loadWorkerConfig reads worker.toml from dir. A missing or empty file returns
// a zero config without error (the bridge uses its defaults).
func loadWorkerConfig(dir string) workerConfig {
	var cfg workerConfig
	path := filepath.Join(dir, "worker.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg // missing → defaults
	}
	if err := toml.Unmarshal(b, &cfg); err != nil {
		log.Printf("worker.toml: parse error: %v (using defaults)", err)
		return cfg
	}
	return cfg
}

type acpBridge struct {
	learnerDir   string
	pool         *acpPool
	monoTaskACP  bool
	taskTimeout  time.Duration // per-delegate_task timeout (default 5m)
	descOverride string       // from worker.toml, overrides delegate_task description

	// Async worker tracking
	workers   map[string]*asyncWorker
	workerSeq int
	workerMu  sync.Mutex

	// Heartbeat configuration
	heartbeatInterval time.Duration // default 5m
	deadTimeout       time.Duration // default 10m
}

// asyncWorker tracks a background worker started via delegate_task_async.
type asyncWorker struct {
	ID        string
	Task      string
	Cwd       string
	Status    string // "running", "done", "failed", "killed"
	Result    string
	Error     string
	StartedAt time.Time
	DoneAt    time.Time
	SessionID string
	client    *acpClient
	cancel    context.CancelFunc
	mu        sync.Mutex // protects Status, Result, Error, LastHeartbeat, LastResponse, HeartbeatOK

	// Heartbeat monitoring
	LastHeartbeat time.Time
	LastResponse  time.Time
	HeartbeatOK   bool
}

// ─── ACP process pool ───────────────────────────────────────────────────

const (
	defaultPoolMaxIdle  = 1
	defaultPoolMaxTotal = 1
	poolIdleTTL         = 60 * time.Second
)

// acpPool manages a set of reusable ACP subprocesses. Each process handles one
// session at a time (serial prompts), but the process itself is reused across
// tasks to avoid the 20-50MB overhead of spawning a fresh Go runtime per call.
type acpPool struct {
	mu       sync.Mutex
	dir      string
	idle     []*acpClient
	active   int
	total    int
	maxIdle  int
	maxTotal int
	stopCh   chan struct{}
	stopped  bool
}

func newACPPool(dir string, maxIdle, maxTotal int) *acpPool {
	if maxIdle <= 0 {
		maxIdle = defaultPoolMaxIdle
	}
	if maxTotal <= 0 {
		maxTotal = defaultPoolMaxTotal
	}
	p := &acpPool{
		dir:      dir,
		maxIdle:  maxIdle,
		maxTotal: maxTotal,
		stopCh:   make(chan struct{}),
	}
	go p.reaper()
	return p
}

// get returns an idle ACP process or spawns a new one. The caller must call
// put() when done (on success) or discard() (on error/timeout).
func (p *acpPool) get() (*acpClient, error) {
	p.mu.Lock()
	// Try to reuse an idle process.
	if len(p.idle) > 0 {
		c := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		p.active++
		p.mu.Unlock()
		log.Printf("pool: reused idle process (active=%d idle=%d total=%d)", p.active, len(p.idle), p.total)
		return c, nil
	}
	// Spawn a new process if under the cap.
	if p.total >= p.maxTotal {
		p.mu.Unlock()
		return nil, fmt.Errorf("acp pool: at capacity (%d/%d)", p.total, p.maxTotal)
	}
	p.total++
	p.active++
	p.mu.Unlock()

	c, err := startACP(p.dir)
	if err != nil {
		p.mu.Lock()
		p.total--
		p.active--
		p.mu.Unlock()
		return nil, err
	}
	log.Printf("pool: spawned new process (active=%d idle=%d total=%d)", p.active, len(p.idle), p.total)
	return c, nil
}

// put returns a healthy process to the idle pool after a successful session.
func (p *acpPool) put(c *acpClient) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active--
	if p.stopped {
		p.total--
		go c.close()
		return
	}
	p.idle = append(p.idle, c)
	log.Printf("pool: returned to idle (active=%d idle=%d total=%d)", p.active, len(p.idle), p.total)
}

// discard marks a process as done without returning it to the pool (error/timeout).
func (p *acpPool) discard(c *acpClient) {
	p.mu.Lock()
	p.total--
	p.active--
	p.mu.Unlock()
	go c.close()
	log.Printf("pool: discarded process (active=%d idle=%d total=%d)", p.active, len(p.idle), p.total)
}

// drain kills all idle processes. Called on bridge shutdown.
func (p *acpPool) drain() {
	p.mu.Lock()
	p.stopped = true
	idle := p.idle
	p.idle = nil
	p.mu.Unlock()
	close(p.stopCh)
	for _, c := range idle {
		c.close()
	}
	log.Printf("pool: drained %d idle processes", len(idle))
}

// reaper periodically closes idle processes that have been sitting too long.
func (p *acpPool) reaper() {
	ticker := time.NewTicker(poolIdleTTL / 2)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.mu.Lock()
			if len(p.idle) <= p.maxIdle {
				p.mu.Unlock()
				continue
			}
			// Kill excess idle processes (keep maxIdle warm).
			excess := p.idle[p.maxIdle:]
			p.idle = p.idle[:p.maxIdle]
			p.total -= len(excess)
			p.mu.Unlock()
			for _, c := range excess {
				c.close()
			}
			log.Printf("pool: reaped %d excess idle processes", len(excess))
		}
	}
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
		resp.Result = map[string]any{"tools": b.toolList()}
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

func (b *acpBridge) toolList() []map[string]any {
	description := "Delegate a task to the learner Reasonix agent. The learner runs autonomously in its own workspace, thinks through the problem, calls tools (read_file, bash, edit_file, etc.), and returns the result. Use this when a task belongs to the learner's workspace or you want a focused sub-agent to handle it."
	if b.descOverride != "" {
		description = b.descOverride
	}
	return []map[string]any{
		{
			"name":        "delegate_task",
			"description": description,
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
		{
			"name":        "delegate_task_async",
			"description": "Start a task on the learner agent without blocking. Returns a worker_id immediately. Use worker_status to check progress and get the result when done. This allows starting multiple workers in parallel and checking them later.",
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
				"title":        "Start async task on learner",
			},
		},
		{
			"name":        "worker_status",
			"description": "Check the status of a background worker started with delegate_task_async. Returns the worker's status (running/done/failed) and result if complete. Does not block.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"worker_id": map[string]any{
						"type":        "string",
						"description": "The worker ID returned by delegate_task_async.",
					},
				},
				"required": []string{"worker_id"},
			},
			"annotations": map[string]any{
				"readOnlyHint": true,
				"title":        "Check worker status",
			},
		},
		{
			"name":        "worker_kill",
			"description": "Kill a running background worker started with delegate_task_async. The worker's ACP session is closed and the process is terminated. Returns the worker's final status.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"worker_id": map[string]any{
						"type":        "string",
						"description": "The worker ID returned by delegate_task_async.",
					},
				},
				"required": []string{"worker_id"},
			},
			"annotations": map[string]any{
				"readOnlyHint": false,
				"title":        "Kill background worker",
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

	switch p.Name {
	case "delegate_task":
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

	case "delegate_task_async":
		task, _ := p.Arguments["task"].(string)
		cwd, _ := p.Arguments["cwd"].(string)

		if task == "" {
			return textResult("argument 'task' is required and must be a non-empty string", true), nil
		}

		log.Printf("delegate_task_async called, task length=%d, cwd=%s", len(task), strOr(cwd, b.learnerDir))

		workerID, err := b.delegateTaskAsync(task, cwd)
		if err != nil {
			return textResult(fmt.Sprintf("error starting async task: %v", err), true), nil
		}
		return textResult(fmt.Sprintf("Worker started: %s. Use worker_status(worker_id=%q) to check progress.", workerID, workerID), false), nil

	case "worker_status":
		workerID, _ := p.Arguments["worker_id"].(string)
		if workerID == "" {
			return textResult("argument 'worker_id' is required", true), nil
		}

		status, err := b.workerStatus(workerID)
		if err != nil {
			return textResult(fmt.Sprintf("error checking worker status: %v", err), true), nil
		}
		return textResult(status, false), nil

	case "worker_kill":
		workerID, _ := p.Arguments["worker_id"].(string)
		if workerID == "" {
			return textResult("argument 'worker_id' is required", true), nil
		}

		status, err := b.workerKill(workerID)
		if err != nil {
			return textResult(fmt.Sprintf("error killing worker: %v", err), true), nil
		}
		return textResult(status, false), nil

	default:
		return nil, &rpcError{Code: codeInvalidParams, Message: "unknown tool: " + p.Name}
	}
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

// cwdKey returns a short, filesystem-safe hash of cwd for keying per-worker
// session state files, so concurrent delegates to different cwd paths don't
// clobber each other.
func cwdKey(cwd string) string {
	h := sha256.Sum256([]byte(cwd))
	return hex.EncodeToString(h[:4]) // 8 hex chars
}

func sessionStatePath(learnerDir, cwd string) string {
	return filepath.Join(learnerDir, ".reasonix_session_"+cwdKey(cwd)+".json")
}

func loadSessionState(learnerDir, cwd string) (*sessionState, error) {
	path := sessionStatePath(learnerDir, cwd)
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

func saveSessionState(learnerDir, cwd string, s *sessionState) error {
	if s == nil {
		os.Remove(sessionStatePath(learnerDir, cwd))
		return nil
	}
	path := sessionStatePath(learnerDir, cwd)
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

	var client *acpClient
	var err error
	if b.monoTaskACP {
		client, err = startACP(b.learnerDir)
		if err != nil {
			return "", err
		}
		defer client.close()
	} else {
		client, err = b.pool.get()
		if err != nil {
			return "", err
		}
	}

	// 1. Initialize
	_, _, err = client.acpCall("initialize", map[string]any{
		"protocolVersion": 1,
		"clientInfo":      map[string]any{"name": "reasonix-manager", "version": version},
	})
	if err != nil {
		if !b.monoTaskACP {
			b.pool.discard(client)
		}
		return "", fmt.Errorf("initialize: %w", err)
	}

	// 2. Try to resume previous session, or create a new one
	sessionID := ""
	transcriptPath := ""

	prevState, err := loadSessionState(b.learnerDir, cwd)
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
			if !b.monoTaskACP {
				b.pool.discard(client)
			}
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

	// 3. Session/prompt with a configurable timeout (default 5 minutes)
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
			if !b.monoTaskACP {
				b.pool.discard(client)
			}
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
		saveSessionState(b.learnerDir, cwd, &sessionState{
			SessionID:      sessionID,
			TranscriptPath: transcriptPath,
			Cwd:            cwd,
		})

		// 6. Format the result
		output := formatResult(notifications)
		log.Printf("session %s done: reason=%s, output=%d chars",
			sessionID, promptResult.StopReason, len(output))

		if !b.monoTaskACP {
			b.pool.put(client)
		}
		return output, nil

	case <-time.After(b.taskTimeout):
		log.Printf("session %s timed out after %s, killing", sessionID, b.taskTimeout)
		if !b.monoTaskACP {
			b.pool.discard(client)
		}
		return "", fmt.Errorf("delegate_task timed out after %s", b.taskTimeout)
	}
}

// ─── Async worker delegation ────────────────────────────────────────────

// delegateTaskAsync starts a worker task without blocking. Returns a worker ID
// that can be used with workerStatus() to check progress and get the result.
func (b *acpBridge) delegateTaskAsync(task, cwd string) (string, error) {
	if cwd == "" {
		cwd = b.learnerDir
	}

	// Get an ACP client from the pool
	client, err := b.pool.get()
	if err != nil {
		return "", err
	}

	// Initialize the ACP session
	_, _, err = client.acpCall("initialize", map[string]any{
		"protocolVersion": 1,
		"clientInfo":      map[string]any{"name": "reasonix-manager-async", "version": version},
	})
	if err != nil {
		b.pool.discard(client)
		return "", fmt.Errorf("initialize: %w", err)
	}

	// Try to resume previous session, or create a new one
	sessionID := ""
	transcriptPath := ""

	prevState, err := loadSessionState(b.learnerDir, cwd)
	if err != nil {
		log.Printf("warning: failed to load session state: %v", err)
	}
	if prevState != nil {
		_, _, err = client.acpCall("session/load", map[string]any{
			"sessionId": prevState.SessionID,
			"cwd":       cwd,
		})
		if err != nil {
			log.Printf("session/load failed (%v), creating new session", err)
			prevState = nil
		} else {
			sessionID = prevState.SessionID
			transcriptPath = prevState.TranscriptPath
		}
	}

	if prevState == nil {
		resultRaw, _, err := client.acpCall("session/new", map[string]any{"cwd": cwd})
		if err != nil {
			b.pool.discard(client)
			return "", fmt.Errorf("session/new: %w", err)
		}
		var sessionResult struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(resultRaw, &sessionResult); err != nil {
			b.pool.discard(client)
			return "", fmt.Errorf("parse session/new result: %w", err)
		}
		sessionID = sessionResult.SessionID
	}

	// Create async worker tracker
	b.workerMu.Lock()
	b.workerSeq++
	workerID := fmt.Sprintf("worker-%d", b.workerSeq)
	ctx, cancel := context.WithCancel(context.Background())
	worker := &asyncWorker{
		ID:            workerID,
		Task:          task,
		Cwd:           cwd,
		Status:        "running",
		StartedAt:     time.Now(),
		LastHeartbeat: time.Now(),
		LastResponse:  time.Now(),
		HeartbeatOK:   true,
		SessionID:     sessionID,
		client:        client,
		cancel:        cancel,
	}
	b.workers[workerID] = worker
	b.workerMu.Unlock()

	// Start heartbeat monitor
	go b.heartbeatMonitor(ctx, worker)

	// Start the worker in a goroutine
	go b.runAsyncWorker(ctx, worker, transcriptPath)

	log.Printf("async worker %s started: task length=%d, cwd=%s, session=%s", workerID, len(task), cwd, sessionID)
	return workerID, nil
}

// runAsyncWorker executes the worker task in the background.
func (b *acpBridge) runAsyncWorker(ctx context.Context, w *asyncWorker, transcriptPath string) {
	defer func() {
		w.cancel()
	}()

	// Send the task prompt
	promptCh := make(chan struct {
		raw    json.RawMessage
		notifs []json.RawMessage
		err    error
	}, 1)

	go func() {
		promptRaw, notifs, perr := w.client.acpCall("session/prompt", map[string]any{
			"sessionId": w.SessionID,
			"prompt":    []map[string]any{{"type": "text", "text": w.Task}},
		})
		promptCh <- struct {
			raw    json.RawMessage
			notifs []json.RawMessage
			err    error
		}{promptRaw, notifs, perr}
	}()

	select {
	case res := <-promptCh:
		if res.err != nil {
			w.client.acpCall("session/close", map[string]any{"sessionId": w.SessionID})
			b.pool.discard(w.client)

			w.mu.Lock()
			w.Status = "failed"
			w.Error = res.err.Error()
			w.DoneAt = time.Now()
			w.mu.Unlock()
			log.Printf("async worker %s failed: %v", w.ID, res.err)
			return
		}

		var promptResult struct {
			StopReason     string  `json:"stopReason"`
			TranscriptPath *string `json:"transcriptPath,omitempty"`
		}
		json.Unmarshal(res.raw, &promptResult)

		if promptResult.TranscriptPath != nil && *promptResult.TranscriptPath != "" {
			transcriptPath = *promptResult.TranscriptPath
		}

		// Close the session
		w.client.acpCall("session/close", map[string]any{"sessionId": w.SessionID})

		// Save session state
		saveSessionState(b.learnerDir, w.Cwd, &sessionState{
			SessionID:      w.SessionID,
			TranscriptPath: transcriptPath,
			Cwd:            w.Cwd,
		})

		// Format and store result
		output := formatResult(res.notifs)
		w.mu.Lock()
		w.Result = output
		w.Status = "done"
		w.DoneAt = time.Now()
		w.mu.Unlock()

		b.pool.put(w.client)
		log.Printf("async worker %s done: reason=%s, output=%d chars", w.ID, promptResult.StopReason, len(output))

	case <-ctx.Done():
		// Context cancelled (kill requested)
		w.client.acpCall("session/close", map[string]any{"sessionId": w.SessionID})
		b.pool.discard(w.client)

		w.mu.Lock()
		w.Status = "killed"
		w.Error = "cancelled"
		w.DoneAt = time.Now()
		w.mu.Unlock()
		log.Printf("async worker %s killed", w.ID)
	}
}

// workerStatus returns the status of an async worker.
func (b *acpBridge) workerStatus(workerID string) (string, error) {
	b.workerMu.Lock()
	worker, ok := b.workers[workerID]
	b.workerMu.Unlock()

	if !ok {
		return "", fmt.Errorf("worker %q not found", workerID)
	}

	worker.mu.Lock()
	defer worker.mu.Unlock()

	var status strings.Builder
	fmt.Fprintf(&status, "[%s] %s", worker.ID, worker.Status)
	fmt.Fprintf(&status, "\nTask: %s", truncateString(worker.Task, 100))
	fmt.Fprintf(&status, "\nCwd: %s", worker.Cwd)
	fmt.Fprintf(&status, "\nStarted: %s", worker.StartedAt.Format(time.RFC3339))

	if worker.Status == "running" {
		elapsed := time.Since(worker.StartedAt)
		fmt.Fprintf(&status, "\nElapsed: %s", elapsed.Round(time.Second))
		fmt.Fprintf(&status, "\nHeartbeat: %s (last response: %s ago)",
			heartbeatStatus(worker.HeartbeatOK),
			time.Since(worker.LastResponse).Round(time.Second))
	} else {
		fmt.Fprintf(&status, "\nDuration: %s", worker.DoneAt.Sub(worker.StartedAt).Round(time.Second))
	}

	if worker.Status == "done" {
		fmt.Fprintf(&status, "\nResult:\n%s", worker.Result)
	} else if worker.Status == "failed" {
		fmt.Fprintf(&status, "\nError: %s", worker.Error)
	}

	return status.String(), nil
}

// heartbeatStatus returns a human-readable heartbeat status.
func heartbeatStatus(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAILED"
}

// workerKill kills a running async worker.
func (b *acpBridge) workerKill(workerID string) (string, error) {
	b.workerMu.Lock()
	worker, ok := b.workers[workerID]
	b.workerMu.Unlock()

	if !ok {
		return "", fmt.Errorf("worker %q not found", workerID)
	}

	worker.mu.Lock()
	status := worker.Status
	worker.mu.Unlock()

	if status != "running" {
		return fmt.Sprintf("[%s] already %s", worker.ID, status), nil
	}

	// Cancel the worker context
	worker.cancel()

	// Wait briefly for cleanup
	time.Sleep(100 * time.Millisecond)

	b.workerMu.Lock()
	worker, ok = b.workers[workerID]
	b.workerMu.Unlock()

	if ok {
		worker.mu.Lock()
		finalStatus := worker.Status
		worker.mu.Unlock()
		return fmt.Sprintf("[%s] killed (was %s)", worker.ID, finalStatus), nil
	}

	return fmt.Sprintf("[%s] kill signal sent", workerID), nil
}

// heartbeatMonitor periodically checks if a worker is alive and kills it if dead.
func (b *acpBridge) heartbeatMonitor(ctx context.Context, w *asyncWorker) {
	ticker := time.NewTicker(b.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check if worker is still running
			w.mu.Lock()
			status := w.Status
			lastResponse := w.LastResponse
			w.mu.Unlock()

			if status != "running" {
				return // worker finished
			}

			// Check for dead worker (no response in deadTimeout)
			if time.Since(lastResponse) > b.deadTimeout {
				log.Printf("heartbeat: worker %s dead (no response in %s), killing", w.ID, b.deadTimeout)
				w.cancel()
				return
			}

			// Send heartbeat ping
			go b.sendHeartbeat(w)
		}
	}
}

// sendHeartbeat pings a worker to check if it's alive.
func (b *acpBridge) sendHeartbeat(w *asyncWorker) {
	// Send an empty prompt as heartbeat
	_, _, err := w.client.acpCall("session/prompt", map[string]any{
		"sessionId": w.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": "__heartbeat__"}},
	})

	w.mu.Lock()
	defer w.mu.Unlock()

	if err != nil {
		w.HeartbeatOK = false
		log.Printf("heartbeat: worker %s failed: %v", w.ID, err)
	} else {
		w.LastHeartbeat = time.Now()
		w.LastResponse = time.Now()
		w.HeartbeatOK = true
	}
}

// truncateString truncates a string to maxLen, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
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
