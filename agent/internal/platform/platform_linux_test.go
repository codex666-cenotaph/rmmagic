//go:build linux

package platform

import "testing"

func TestParseTSV(t *testing.T) {
	in := []byte("bash\t5.2-2\tamd64\nzlib1g\t1.3\t\nbroken-line\n\tno-name\t1\n")
	pkgs := parseTSV(in)
	if len(pkgs) != 2 {
		t.Fatalf("got %d packages, want 2: %+v", len(pkgs), pkgs)
	}
	if pkgs[0].Name != "bash" || pkgs[0].Version != "5.2-2" || pkgs[0].Arch != "amd64" {
		t.Errorf("unexpected first package: %+v", pkgs[0])
	}
	if pkgs[1].Name != "zlib1g" || pkgs[1].Arch != "" {
		t.Errorf("unexpected second package: %+v", pkgs[1])
	}
}

func TestParseTSVEmpty(t *testing.T) {
	if pkgs := parseTSV(nil); pkgs == nil || len(pkgs) != 0 {
		t.Fatalf("want empty non-nil slice, got %#v", pkgs)
	}
}
