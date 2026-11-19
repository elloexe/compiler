package forge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectLanguage(t *testing.T) {
	cases := map[string][]string{
		"c":    {"a.c", "b.h"},
		"c++":  {"a.c", "b.cpp"},
		"go":   {"main.go"},
		"rust": {"main.rs"},
	}
	for want, srcs := range cases {
		if got, err := DetectLanguage(srcs); err != nil || got != want {
			t.Errorf("%v: got %q, %v; want %q", srcs, got, err, want)
		}
	}
	if _, err := DetectLanguage([]string{"a.go", "b.c"}); err == nil {
		t.Error("mixing go and c should fail")
	}
	if _, err := DetectLanguage([]string{"a.py"}); err == nil {
		t.Error(".py should be rejected")
	}
}

func TestBuildTemplates(t *testing.T) {
	if _, err := FindToolchain("auto", ArchX64); err != nil {
		t.Skip("no Windows C toolchain: ", err)
	}
	dir := t.TempDir()
	for _, tpl := range Templates {
		if tpl.Filename[len(tpl.Filename)-3:] == ".go" {
			continue
		}
		src := filepath.Join(dir, tpl.Filename)
		os.WriteFile(src, []byte(tpl.Code), 0o644)
		var libs []string
		if tpl.Libs != "" {
			libs = append(libs, splitFields(tpl.Libs)...)
		}
		res, err := Build(Options{Sources: []string{src}, GUI: tpl.GUI, Libs: libs, Log: func(s string) { t.Log(s) }})
		if err != nil {
			t.Fatalf("%s: %v", tpl.ID, err)
		}
		info, err := InspectPE(res.Output)
		if err != nil {
			t.Fatal(err)
		}
		wantSub := map[bool]string{true: "GUI", false: "console"}[tpl.GUI]
		if info.Machine != "x64" || info.Subsystem != wantSub {
			t.Errorf("%s: got %s/%s", tpl.ID, info.Machine, info.Subsystem)
		}
	}
}

func splitFields(s string) []string {
	var out []string
	cur := ""
	for _, r := range s + " " {
		if r == ' ' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
		} else {
			cur += string(r)
		}
	}
	return out
}
