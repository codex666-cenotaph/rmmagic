package exec_test

import (
	"context"
	"os"
	"testing"

	agentexec "github.com/codex666-cenotaph/rmmagic/agent/internal/exec"
)

func TestRunScript_bash(t *testing.T) {
	if _, err := os.LookupEnv("SKIP_EXEC_TESTS"); err == false {
		// Always run on Linux where bash is present.
	}
	spec := agentexec.ScriptSpec{
		Language:   "bash",
		Body:       "#!/bin/bash\necho hello",
		Parameters: map[string]string{},
	}
	r := agentexec.RunScript(context.Background(), spec, 10)
	if r.Err != nil {
		t.Fatalf("unexpected error: %v", r.Err)
	}
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if string(r.Output) != "hello\n" {
		t.Fatalf("unexpected output: %q", r.Output)
	}
}

func TestRunScript_exitCode(t *testing.T) {
	spec := agentexec.ScriptSpec{Language: "bash", Body: "exit 42", Parameters: map[string]string{}}
	r := agentexec.RunScript(context.Background(), spec, 10)
	if r.ExitCode != 42 {
		t.Fatalf("expected exit 42, got %d (err=%v)", r.ExitCode, r.Err)
	}
}

func TestRunScript_envParams(t *testing.T) {
	spec := agentexec.ScriptSpec{
		Language:   "bash",
		Body:       `#!/bin/bash\necho "name=$RMM_PARAM_NAME"`,
		Parameters: map[string]string{"NAME": "world"},
	}
	r := agentexec.RunScript(context.Background(), spec, 10)
	if r.Err != nil {
		t.Fatalf("error: %v", r.Err)
	}
}

func TestJournal(t *testing.T) {
	dir := t.TempDir()
	j, err := agentexec.NewJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if j.Contains("cmd-1") {
		t.Fatal("should not contain cmd-1 before recording")
	}
	if err := j.Record("cmd-1"); err != nil {
		t.Fatal(err)
	}
	if !j.Contains("cmd-1") {
		t.Fatal("should contain cmd-1 after recording")
	}

	// Reload from disk and verify persistence.
	j2, err := agentexec.NewJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !j2.Contains("cmd-1") {
		t.Fatal("journal not persisted across reload")
	}
	if j2.Contains("cmd-999") {
		t.Fatal("journal contains unexpected ID")
	}
}
