package codex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
)

func TestQueueCommand(t *testing.T) {
	t.Parallel()

	config := bridgeconfig.Config{Runtime: bridgeconfig.RuntimeConfig{
		Kind:     bridgeconfig.RuntimeCodex,
		Mode:     bridgeconfig.RuntimeModeAttached,
		Session:  "lmc-work",
		Command:  "/opt/bin/codex",
		HomePath: "/home/agent",
	}}
	spec := QueueCommand(config, "[moltnet] 1 new message in #r. Read: moltnet read --since-last room:r")
	if spec.Command != "/opt/bin/codex" {
		t.Fatalf("command = %q", spec.Command)
	}
	wantArgs := []string{"queue", "--thread", "lmc-work", "--message", "[moltnet] 1 new message in #r. Read: moltnet read --since-last room:r"}
	if !reflect.DeepEqual(spec.Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", spec.Args, wantArgs)
	}
	if !reflect.DeepEqual(spec.Env, []string{"CODEX_HOME=/home/agent/.codex"}) {
		t.Fatalf("env = %#v", spec.Env)
	}
	if spec.Stdin != "" || spec.Timeout <= 0 {
		t.Fatalf("unexpected stdin/timeout %q %v", spec.Stdin, spec.Timeout)
	}

	config.Runtime.Command = ""
	config.Runtime.HomePath = ""
	spec = QueueCommand(config, "x")
	if spec.Command != "codex" || len(spec.Env) != 0 {
		t.Fatalf("defaults: command %q env %#v", spec.Command, spec.Env)
	}
}

func TestAttachedNotifierRunsQueueCommand(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	script := filepath.Join(dir, "codex")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsPath + "\n[ \"$3\" = missing ] && { echo 'no such thread' >&2; exit 3; }\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	config := bridgeconfig.Config{Runtime: bridgeconfig.RuntimeConfig{Kind: bridgeconfig.RuntimeCodex, Command: script, Session: "lmc-work"}}
	if err := (AttachedNotifier{}).Notify(context.Background(), config, "hi there"); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	got, _ := os.ReadFile(argsPath)
	if string(got) != "queue\n--thread\nlmc-work\n--message\nhi there\n" {
		t.Fatalf("args = %q", got)
	}

	config.Runtime.Session = "missing"
	err := (AttachedNotifier{}).Notify(context.Background(), config, "hi")
	if err == nil || !strings.Contains(err.Error(), "no such thread") {
		t.Fatalf("Notify() failing command error = %v", err)
	}
}
