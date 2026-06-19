package cahooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// Manager coordinates cahooks execution for a session.
type Manager struct {
	cfg     *Config
	prov    provider.Provider
	sink    event.Sink
	debug   bool
	mu      sync.Mutex
	running int // number of async hooks currently running
}

// NewManager creates a cahooks manager.
func NewManager(cfg *Config, prov provider.Provider, sink event.Sink) *Manager {
	return &Manager{cfg: cfg, prov: prov, sink: sink, debug: cfg != nil && cfg.Debug}
}

// RunHooks fires all enabled hooks matching the given trigger and state.
// Async hooks run in goroutines; sync hooks block.
func (m *Manager) RunHooks(ctx context.Context, trigger string, state State, session []provider.Message, cwd string) {
	if m == nil || m.cfg == nil {
		return
	}
	hooks := m.cfg.ForTrigger(trigger)
	for _, hook := range hooks {
		if !ShouldFire(hook, state) {
			continue
		}
		if hook.Async {
			go m.runOne(ctx, hook, state, session, cwd)
		} else {
			m.runOne(ctx, hook, state, session, cwd)
		}
	}
}

// RunHooksSync fires all matching hooks synchronously and waits for completion.
func (m *Manager) RunHooksSync(ctx context.Context, trigger string, state State, session []provider.Message, cwd string) {
	if m == nil || m.cfg == nil {
		return
	}
	hooks := m.cfg.ForTrigger(trigger)
	var wg sync.WaitGroup
	for _, hook := range hooks {
		if !ShouldFire(hook, state) {
			continue
		}
		wg.Add(1)
		go func(h HookConfig) {
			defer wg.Done()
			m.runOne(ctx, h, state, session, cwd)
		}(hook)
	}
	wg.Wait()
}

func (m *Manager) runOne(ctx context.Context, hook HookConfig, state State, session []provider.Message, cwd string) {
	m.mu.Lock()
	m.running++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.running--
		m.mu.Unlock()
	}()

	timeout := time.Duration(hook.TimeoutMS) * time.Millisecond
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startTime := time.Now()

	// Debug: emit start event
	if m.debug && m.sink != nil {
		m.sink.Emit(event.Event{
			Kind: event.ToolDispatch,
			Tool: event.Tool{
				ID:   fmt.Sprintf("cahook-%s-%d", hook.Name, state.Turn),
				Name: fmt.Sprintf("cahook:%s", hook.Name),
				Args: fmt.Sprintf(`{"trigger":"%s","turn":%d}`, hook.Trigger, state.Turn),
			},
		})
	}

	// Build payload
	payload := BuildPayload(
		hook.Trigger,
		&hook,
		state.Turn,
		"",
		state.ContextUsage,
		0,
		session,
		nil, nil, "", nil,
		cwd,
	)

	// Step 1: Run beforeLLMCall script
	effectivePrompt, effectiveStructure := hook.LLM.Prompt, hook.LLM.OutputStructure
	if hook.BeforeLLMCall != "" {
		ep, es, err := runBeforeLLMCall(ctx, hook, payload)
		if err == nil {
			effectivePrompt = ep
			if es != nil {
				effectiveStructure = es
			}
		}
		// On error, fall back to defaults (don't block)
	}

	// Step 2: Build analysis prompt
	analysisPrompt := BuildAnalysisPrompt(effectivePrompt, effectiveStructure)

	// Step 3: Call LLM
	response, err := CallAnalysis(ctx, m.prov, session, analysisPrompt)
	if err != nil {
		// Debug: emit error result
		if m.debug && m.sink != nil {
			m.sink.Emit(event.Event{
				Kind: event.ToolResult,
				Tool: event.Tool{
					ID:         fmt.Sprintf("cahook-%s-%d", hook.Name, state.Turn),
					Name:       fmt.Sprintf("cahook:%s", hook.Name),
					Err:        err.Error(),
					DurationMs: time.Since(startTime).Milliseconds(),
				},
			})
		}
		return // analysis failed, skip silently
	}

	// Step 4: Write to tmp file
	tmpFile, err := WriteTmp(response)
	if err != nil {
		// Debug: emit error result
		if m.debug && m.sink != nil {
			m.sink.Emit(event.Event{
				Kind: event.ToolResult,
				Tool: event.Tool{
					ID:         fmt.Sprintf("cahook-%s-%d", hook.Name, state.Turn),
					Name:       fmt.Sprintf("cahook:%s", hook.Name),
					Err:        err.Error(),
					DurationMs: time.Since(startTime).Milliseconds(),
				},
			})
		}
		return
	}
	defer CleanupTmp(tmpFile)

	// Step 5: Run afterLLMCall script
	if hook.AfterLLMCall != "" {
		runAfterLLMCall(ctx, hook, payload, tmpFile)
	} else {
		// No after script — write raw LLM output directly
		var analysis map[string]any
		if err := json.Unmarshal([]byte(response), &analysis); err != nil {
			analysis = map[string]any{"raw": response}
		}
		WriteInsight(hook.Output.Dir, hook.Output.File, payload.SessionID, hook.Name, state.Turn, analysis)
	}

	// Debug: emit success result
	if m.debug && m.sink != nil {
		m.sink.Emit(event.Event{
			Kind: event.ToolResult,
			Tool: event.Tool{
				ID:         fmt.Sprintf("cahook-%s-%d", hook.Name, state.Turn),
				Name:       fmt.Sprintf("cahook:%s", hook.Name),
				Output:     fmt.Sprintf("insight written to %s/%s", hook.Output.Dir, hook.Output.File),
				DurationMs: time.Since(startTime).Milliseconds(),
			},
		})
	}
}

func runBeforeLLMCall(ctx context.Context, hook HookConfig, payload Payload) (string, map[string]any, error) {
	input := BeforeLLMCallInput{
		Payload: payload,
	}
	input.Config.Prompt = hook.LLM.Prompt
	input.Config.OutputStructure = hook.LLM.OutputStructure

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return hook.LLM.Prompt, hook.LLM.OutputStructure, err
	}

	stdout, err := runScript(ctx, hook.BeforeLLMCall, string(inputJSON), payload.Cwd)
	if err != nil {
		return hook.LLM.Prompt, hook.LLM.OutputStructure, err
	}

	var output BeforeLLMCallOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		return hook.LLM.Prompt, hook.LLM.OutputStructure, err
	}

	prompt := output.UserPrompt
	if prompt == "" {
		prompt = hook.LLM.Prompt
	}
	structure := output.OutputStructure
	if structure == nil {
		structure = hook.LLM.OutputStructure
	}
	return prompt, structure, nil
}

func runAfterLLMCall(ctx context.Context, hook HookConfig, payload Payload, tmpFile string) {
	input := AfterLLMCallInput{
		Payload: payload,
		TmpFile: tmpFile,
	}

	// Read tmp file content for the script
	data, err := readFileContent(tmpFile)
	if err == nil {
		var parsed any
		if json.Unmarshal([]byte(data), &parsed) == nil {
			input.LLMOutput = parsed
		}
	}

	inputJSON, _ := json.Marshal(input)
	runScript(ctx, hook.AfterLLMCall, string(inputJSON), payload.Cwd)
}

func runScript(ctx context.Context, script, stdin, cwd string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Stdin = strings.NewReader(stdin)
	if cwd != "" {
		cmd.Dir = cwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("script %s: %w (stderr: %s)", script, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

func readFileContent(path string) (string, error) {
	data, err := readFile(path)
	return string(data), err
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
