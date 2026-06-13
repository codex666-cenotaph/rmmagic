//go:build windows

package platform

import (
	"errors"
	"sort"
	"testing"

	"golang.org/x/sys/windows/svc"
)

// fakeKey is an in-memory registryKey for testing without touching the real registry.
type fakeKey struct {
	subkeys  map[string]*fakeKey
	strings  map[string]string
	integers map[string]uint64
}

func (f *fakeKey) Close() error { return nil }

func (f *fakeKey) ReadSubKeyNames(_ int) ([]string, error) {
	names := make([]string, 0, len(f.subkeys))
	for k := range f.subkeys {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

func (f *fakeKey) OpenSubKey(path string) (registryKey, error) {
	k, ok := f.subkeys[path]
	if !ok {
		return nil, errors.New("key not found")
	}
	return k, nil
}

func (f *fakeKey) GetStringValue(name string) (string, error) {
	v, ok := f.strings[name]
	if !ok {
		return "", errors.New("value not found")
	}
	return v, nil
}

func (f *fakeKey) GetIntegerValue(name string) (uint64, error) {
	v, ok := f.integers[name]
	if !ok {
		return 0, errors.New("value not found")
	}
	return v, nil
}

func fakeOpener(root *fakeKey) func(string) (registryKey, error) {
	return func(_ string) (registryKey, error) { return root, nil }
}

func TestReadRegistryPackages_Basic(t *testing.T) {
	root := &fakeKey{
		subkeys: map[string]*fakeKey{
			"pkg-a": {strings: map[string]string{"DisplayName": "App A", "DisplayVersion": "1.2.3"}},
			"pkg-b": {strings: map[string]string{"DisplayName": "App B"}}, // no version
		},
	}
	pkgs := readRegistryPackages(fakeOpener(root), "")
	if len(pkgs) != 2 {
		t.Fatalf("want 2 packages, got %d: %+v", len(pkgs), pkgs)
	}
	// sorted by subkey name
	if pkgs[0].Name != "App A" || pkgs[0].Version != "1.2.3" {
		t.Errorf("unexpected pkg[0]: %+v", pkgs[0])
	}
	if pkgs[1].Name != "App B" || pkgs[1].Version != "" {
		t.Errorf("unexpected pkg[1]: %+v", pkgs[1])
	}
}

func TestReadRegistryPackages_SkipsSystemComponent(t *testing.T) {
	root := &fakeKey{
		subkeys: map[string]*fakeKey{
			"sys": {
				strings:  map[string]string{"DisplayName": "Hidden Component"},
				integers: map[string]uint64{"SystemComponent": 1},
			},
			"app": {strings: map[string]string{"DisplayName": "Real App", "DisplayVersion": "2.0"}},
		},
	}
	pkgs := readRegistryPackages(fakeOpener(root), "")
	if len(pkgs) != 1 || pkgs[0].Name != "Real App" {
		t.Fatalf("want 1 non-system package, got %+v", pkgs)
	}
}

func TestReadRegistryPackages_SkipsEmptyName(t *testing.T) {
	root := &fakeKey{
		subkeys: map[string]*fakeKey{
			"no-name":  {strings: map[string]string{}},
			"has-name": {strings: map[string]string{"DisplayName": "Valid"}},
		},
	}
	pkgs := readRegistryPackages(fakeOpener(root), "")
	if len(pkgs) != 1 || pkgs[0].Name != "Valid" {
		t.Fatalf("want 1 package with name, got %+v", pkgs)
	}
}

func TestReadRegistryPackages_OpenerError(t *testing.T) {
	fail := func(_ string) (registryKey, error) { return nil, errors.New("access denied") }
	pkgs := readRegistryPackages(fail, "")
	if pkgs != nil {
		t.Fatalf("want nil on opener error, got %+v", pkgs)
	}
}

func TestCollectPackages_Deduplication(t *testing.T) {
	// Simulate the same package appearing in both hives (64-bit and 32-bit).
	shared := &fakeKey{strings: map[string]string{"DisplayName": "SharedApp", "DisplayVersion": "3.0"}}
	only64 := &fakeKey{strings: map[string]string{"DisplayName": "Only64", "DisplayVersion": "1.0"}}
	only32 := &fakeKey{strings: map[string]string{"DisplayName": "Only32", "DisplayVersion": "2.0"}}

	hive64 := &fakeKey{subkeys: map[string]*fakeKey{"shared": shared, "64bit": only64}}
	hive32 := &fakeKey{subkeys: map[string]*fakeKey{"shared": shared, "32bit": only32}}

	call := 0
	orig := openUninstallRoot
	t.Cleanup(func() { openUninstallRoot = orig })
	openUninstallRoot = func(_ string) (registryKey, error) {
		call++
		if call == 1 {
			return hive64, nil
		}
		return hive32, nil
	}

	pkgs, err := CollectPackages(nil) //nolint:staticcheck // context unused on Windows
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// SharedApp must appear only once; Only64 and Only32 each once → total 3.
	if len(pkgs) != 3 {
		t.Fatalf("want 3 packages, got %d: %+v", len(pkgs), pkgs)
	}
}

func TestMapServiceState(t *testing.T) {
	cases := []struct {
		state svc.State
		want  string
	}{
		{svc.Running, "running"},
		{svc.Stopped, "stopped"},
		{svc.Paused, "paused"},
		{svc.StartPending, "start_pending"},
		{svc.StopPending, "stop_pending"},
		{svc.PausePending, "pause_pending"},
		{svc.ContinuePending, "continue_pending"},
		{svc.State(99), "unknown"},
	}
	for _, c := range cases {
		if got := mapServiceState(c.state); got != c.want {
			t.Errorf("mapServiceState(%d) = %q, want %q", c.state, got, c.want)
		}
	}
}
