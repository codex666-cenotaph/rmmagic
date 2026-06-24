// Package exec runs agent script commands with timeout and output capture,
// maintaining a journal of completed command IDs for idempotent redelivery.
package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/codex666-cenotaph/rmmagic/agent/internal/platform"
)

const outputCapBytes = 1 << 20 // 1 MiB

// ScriptSpec is the JSON payload of COMMAND_KIND_SCRIPT.
type ScriptSpec struct {
	Language   string            `json:"language"`
	Body       string            `json:"body"`
	Parameters map[string]string `json:"parameters"`
}

// Result is the outcome of a script execution.
type Result struct {
	Output     []byte
	Truncated  bool
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	Err        error // non-nil on timeout or exec failure (not non-zero exit)
}

// ParseSpec decodes a COMMAND_KIND_SCRIPT spec from its raw JSON bytes.
func ParseSpec(raw []byte) (ScriptSpec, error) {
	var spec ScriptSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return spec, err
	}
	return spec, nil
}

// PackageSpec is the JSON payload of COMMAND_KIND_PACKAGE_INSTALL and
// COMMAND_KIND_PACKAGE_REMOVE. The install/remove choice is the command
// kind, not a field, so a malformed spec can't flip the operation.
type PackageSpec struct {
	Packages []string `json:"packages"`
}

// ParsePackageSpec decodes a package command spec from its raw JSON bytes.
func ParsePackageSpec(raw []byte) (PackageSpec, error) {
	var spec PackageSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return spec, err
	}
	return spec, nil
}

// RunPackage installs or removes the spec's packages via the host package
// manager, capping output the same way scripts are. install selects
// install vs remove.
func RunPackage(ctx context.Context, spec PackageSpec, install bool, timeoutS uint32) Result {
	if timeoutS == 0 {
		timeoutS = 600
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutS)*time.Second)
	defer cancel()

	if len(spec.Packages) == 0 {
		return Result{Output: []byte("no packages specified"), ExitCode: 1, Err: errors.New("empty package list")}
	}

	started := time.Now()
	var out []byte
	var err error
	if install {
		out, err = platform.InstallPackages(runCtx, spec.Packages)
	} else {
		out, err = platform.RemovePackages(runCtx, spec.Packages)
	}
	finished := time.Now()

	truncated := false
	if len(out) > outputCapBytes {
		out = out[:outputCapBytes]
		truncated = true
	}
	r := Result{Output: out, Truncated: truncated, StartedAt: started, FinishedAt: finished}

	if err != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			r.Err = context.DeadlineExceeded
		case errors.As(err, &exitErr):
			// Package manager ran but returned non-zero (e.g. package not
			// found); surface the exit code, not an exec failure.
			r.ExitCode = exitErr.ExitCode()
		default:
			// Manager missing / could not start: a real execution failure.
			r.Err = err
			if len(r.Output) == 0 {
				r.Output = []byte(err.Error())
			}
		}
	}
	return r
}

// RunScript executes the script described by spec and returns the result.
// It respects ctx for cancellation but also honours timeoutS independently.
func RunScript(ctx context.Context, spec ScriptSpec, timeoutS uint32) Result {
	if timeoutS == 0 {
		timeoutS = 300
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutS)*time.Second)
	defer cancel()

	tmp, err := os.CreateTemp("", "rmm-script-*"+langExtension(spec.Language))
	if err != nil {
		return Result{Err: err}
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.WriteString(spec.Body); err != nil {
		tmp.Close()
		return Result{Err: err}
	}
	if err := tmp.Close(); err != nil {
		return Result{Err: err}
	}
	if err := os.Chmod(tmpPath, 0700); err != nil {
		return Result{Err: err}
	}

	argv, err := buildArgv(spec.Language, tmpPath)
	if err != nil {
		return Result{Err: err}
	}

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Env = buildEnv(spec.Parameters)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	started := time.Now()
	runErr := cmd.Run()
	finished := time.Now()

	output := buf.Bytes()
	truncated := false
	if len(output) > outputCapBytes {
		output = output[:outputCapBytes]
		truncated = true
	}

	r := Result{
		Output:     output,
		Truncated:  truncated,
		StartedAt:  started,
		FinishedAt: finished,
	}

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			r.ExitCode = exitErr.ExitCode()
		} else if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			r.Err = context.DeadlineExceeded
		} else {
			r.Err = runErr
		}
	}
	return r
}

func langExtension(lang string) string {
	switch lang {
	case "bash":
		return ".sh"
	case "python":
		return ".py"
	case "powershell":
		return ".ps1"
	case "batch":
		return ".bat"
	}
	return ".sh"
}

func buildArgv(lang, path string) ([]string, error) {
	switch lang {
	case "bash":
		return []string{"bash", path}, nil
	case "python":
		return []string{"python3", path}, nil
	case "powershell":
		return []string{"powershell", "-NonInteractive", "-File", path}, nil
	case "batch":
		return []string{"cmd.exe", "/c", path}, nil
	}
	return nil, errors.New("unsupported language: " + lang)
}

func buildEnv(params map[string]string) []string {
	env := os.Environ()
	for k, v := range params {
		env = append(env, "RMM_PARAM_"+k+"="+v)
	}
	return env
}

// Journal persists executed command IDs so the agent never runs the same
// command twice, even after a crash-and-reconnect cycle.
type Journal struct {
	mu   sync.Mutex
	path string
	done map[string]bool
}

func NewJournal(stateDir string) (*Journal, error) {
	j := &Journal{
		path: filepath.Join(stateDir, "journal.json"),
		done: map[string]bool{},
	}
	data, err := os.ReadFile(j.path)
	if err != nil {
		if os.IsNotExist(err) {
			return j, nil
		}
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil, err
	}
	for _, id := range ids {
		j.done[id] = true
	}
	return j, nil
}

// Contains reports whether commandID has already been executed.
func (j *Journal) Contains(commandID string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done[commandID]
}

// Record marks commandID as executed and persists the journal to disk.
func (j *Journal) Record(commandID string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.done[commandID] = true
	ids := make([]string, 0, len(j.done))
	for id := range j.done {
		ids = append(ids, id)
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	tmp := j.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, j.path)
}
