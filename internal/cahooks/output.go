package cahooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Insight is a single analysis record written to the output file.
type Insight struct {
	Session  string         `json:"session"`
	Turn     int            `json:"turn"`
	Hook     string         `json:"hook"`
	Created  time.Time      `json:"created_at"`
	LastUsed *time.Time     `json:"last_used_at,omitempty"`
	Analysis map[string]any `json:"analysis"`
}

// AppendJSONL appends a record to a JSONL file, creating the directory and
// file if they don't exist.
func AppendJSONL(dir, file string, record any) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, file)
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// WriteInsight creates an Insight record and appends it to the output file.
func WriteInsight(dir, file, session, hookName string, turn int, analysis map[string]any) error {
	insight := Insight{
		Session:  session,
		Turn:     turn,
		Hook:     hookName,
		Created:  time.Now(),
		Analysis: analysis,
	}
	return AppendJSONL(dir, file, insight)
}

// WriteRaw writes the raw LLM output string as a JSONL record.
func WriteRaw(dir, file, session, hookName string, turn int, raw string) error {
	var analysis map[string]any
	if err := json.Unmarshal([]byte(raw), &analysis); err != nil {
		// If not valid JSON, wrap it as a string value
		analysis = map[string]any{"raw": raw}
	}
	return WriteInsight(dir, file, session, hookName, turn, analysis)
}

// WriteTmp writes content to a temporary file and returns the path.
func WriteTmp(content string) (string, error) {
	f, err := os.CreateTemp("", "reasonix-cahooks-*.json")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// CleanupTmp removes a temp file (no-op if empty path).
func CleanupTmp(path string) {
	if path != "" {
		os.Remove(path)
	}
}
