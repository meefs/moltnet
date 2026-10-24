package claudecode

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	permissionModeBypass = "bypassPermissions"
	permissionTailBytes  = 1 << 20
)

var transcriptSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)

// transcriptPath locates a session's transcript:
// <config>/projects/<cwd with "/" replaced by "-">/<sessionId>.jsonl. A
// conversation started elsewhere may live under another project directory, so
// a single match for the session id under projects/ is used instead.
func transcriptPath(configDir string, cwd string, sessionID string) string {
	if !filepath.IsAbs(cwd) || !transcriptSessionIDPattern.MatchString(sessionID) {
		return ""
	}
	projects := filepath.Join(configDir, "projects")
	expected := filepath.Join(projects, strings.ReplaceAll(cwd, "/", "-"), sessionID+".jsonl")
	if isRegularFile(expected) {
		return expected
	}
	matches, _ := filepath.Glob(filepath.Join(projects, "*", sessionID+".jsonl"))
	if len(matches) == 1 && isRegularFile(matches[0]) {
		return matches[0]
	}
	return ""
}

// latestPermissionMode returns the last "permissionMode" the transcript
// records, reading only its tail; "" when unreadable or absent.
func latestPermissionMode(path string) string {
	if path == "" || !isRegularFile(path) {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	start := info.Size() - permissionTailBytes
	if start < 0 {
		start = 0
	}
	tail, err := io.ReadAll(io.NewSectionReader(file, start, info.Size()-start))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(tail), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if !strings.Contains(lines[index], `"permissionMode"`) {
			continue
		}
		var row struct {
			PermissionMode string `json:"permissionMode"`
		}
		// A cut first line or a half-written last line simply fails to parse.
		if json.Unmarshal([]byte(lines[index]), &row) == nil && row.PermissionMode != "" {
			return row.PermissionMode
		}
	}
	return ""
}

func isRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}
