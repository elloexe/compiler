package forge

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Options controls one build.
type Options struct {
	Sources     []string // source files (or a single Go package directory)
	Output      string   // output .exe path (default: <first source>.exe)
	Arch        Arch
	GUI         bool   // Windows GUI subsystem (no console window)
	Icon        string // .ico file to embed
	ProductName string // embedded version info
	Version     string // e.g. 1.2.3
	Debug       bool   // -g -O0 instead of -O2 -s
	Toolchain   string // auto | mingw | zig
	Defines     []string
	Includes    []string
	Libs        []string // e.g. user32, gdi32, ws2_32
	ExtraFlags  []string
	Log         func(string)
}

// Result of a successful build.
type Result struct {
	Output   string
	Size     int64
	Language string
	Compiler string
	Duration time.Duration
}

func (o *Options) log(format string, a ...any) {
	if o.Log != nil {
		o.Log(fmt.Sprintf(format, a...))
	}
}

var cxxExts = map[string]bool{".cpp": true, ".cc": true, ".cxx": true, ".c++": true, ".cp": true, ".C": true}

// DetectLanguage returns "c", "c++", "go" or "rust".
func DetectLanguage(sources []string) (string, error) {
	if len(sources) == 0 {
		return "", errors.New("no source files given")
	}
	lang := ""
	for _, s := range sources {
		if st, err := os.Stat(s); err == nil && st.IsDir() {
			if m, _ := filepath.Glob(filepath.Join(s, "*.go")); len(m) > 0 {
				return "go", nil
			}
			return "", fmt.Errorf("%s is a directory without .go files", s)
		}
		ext := filepath.Ext(s)
		var l string
		switch {
		case ext == ".c":
			l = "c"
		case cxxExts[ext]:
			l = "c++"
		case ext == ".go":
			l = "go"
		case ext == ".rs":
			l = "rust"
		case ext == ".rc" || ext == ".o" || ext == ".a" || ext == ".h" || ext == ".hpp" || ext == ".res":
			continue
		default:
			return "", fmt.Errorf("don't know how to compile %s (supported: .c .cpp .cc .cxx .go .rs)", s)
		}
		switch {
		case lang == "" || lang == l:
			lang = l
		case (lang == "c" && l == "c++") || (lang == "c++" && l == "c"):
			lang = "c++"
		default:
			return "", fmt.Errorf("cannot mix %s and %s sources in one build", lang, l)
		}
	}
	if lang == "" {
		return "", errors.New("no compilable source files given")
	}
	return lang, nil
}

// Build compiles the sources into a Windows .exe.
func Build(o Options) (*Result, error) {
	start := time.Now()
	if o.Arch == "" {
		o.Arch = ArchX64
	}
	if o.Toolchain == "" {
		o.Toolchain = "auto"
	}
	for _, s := range o.Sources {
		if _, err := os.Stat(s); err != nil {
			return nil, fmt.Errorf("source not found: %s", s)
		}
	}
	lang, err := DetectLanguage(o.Sources)
	if err != nil {
		return nil, err
	}
	if o.Output == "" {
		first := o.Sources[0]
		base := strings.TrimSuffix(filepath.Base(first), filepath.Ext(first))
		if st, _ := os.Stat(first); st != nil && st.IsDir() {
			abs, _ := filepath.Abs(first)
			base = filepath.Base(abs)
			first = filepath.Join(first, "x")
		}
		o.Output = filepath.Join(filepath.Dir(first), base+".exe")
	}
	if !strings.HasSuffix(strings.ToLower(o.Output), ".exe") {
		o.Output += ".exe"
	}
	if abs, err := filepath.Abs(o.Output); err == nil {
		o.Output = abs
	}
	if err := os.MkdirAll(filepath.Dir(o.Output), 0o755); err != nil {
		return nil, err
	}
	if o.Icon != "" {
		if _, err := os.Stat(o.Icon); err != nil {
			return nil, fmt.Errorf("icon not found: %s", o.Icon)
		}
	}

	work, err := os.MkdirTemp("", "exeforge-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	o.log("Language: %s   Target: Windows %s %s", lang, o.Arch, map[bool]string{true: "GUI", false: "console"}[o.GUI])
	var compiler string
	switch lang {
	case "c", "c++":
		compiler, err = buildC(&o, lang, work)
	case "go":
		compiler, err = buildGo(&o, work)
	case "rust":
		compiler, err = buildRust(&o)
	}
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(o.Output)
	if err != nil {
		return nil, fmt.Errorf("compiler finished but %s was not produced", o.Output)
	}
	info, err := InspectPE(o.Output)
	if err != nil {
		return nil, fmt.Errorf("output is not a valid Windows executable: %v", err)
	}
	o.log("✔ Built %s (%s, %s, %s)", o.Output, humanSize(st.Size()), info.Machine, info.Subsystem)
	return &Result{Output: o.Output, Size: st.Size(), Language: lang, Compiler: compiler, Duration: time.Since(start)}, nil
}

func buildC(o *Options, lang, work string) (string, error) {
	tc, err := FindToolchain(o.Toolchain, o.Arch)
	if err != nil {
		return "", err
	}
	o.log("Compiler: %s", tc.Version)

	var args []string
	cc := tc.CC
	if tc.Kind == "zig" {
		sub := "cc"
		if lang == "c++" {
			sub = "c++"
		}
		args = append(args, sub, "-target", o.Arch.zigTarget())
	} else if lang == "c++" {
		if tc.CXX == "" {
			return "", errors.New("MinGW C++ compiler (g++) not found; install g++-mingw-w64")
		}
		cc = tc.CXX
	}
	if o.Debug {
		args = append(args, "-g", "-O0")
	} else {
		args = append(args, "-O2", "-s")
	}
	if tc.Kind == "mingw" {
		// Self-contained exe: no libgcc/libstdc++/winpthread DLLs needed.
		args = append(args, "-static", "-static-libgcc")
		// Reproducible output: same source + same compiler = byte-identical exe.
		args = append(args, "-Wl,--no-insert-timestamp")
		if lang == "c++" {
			args = append(args, "-static-libstdc++")
		}
	}
	if o.GUI {
		args = append(args, "-mwindows")
	} else {
		args = append(args, "-mconsole")
	}
	for _, d := range o.Defines {
		args = append(args, "-D"+d)
	}
	for _, i := range o.Includes {
		args = append(args, "-I"+i)
	}
	args = append(args, o.ExtraFlags...)
	args = append(args, "-o", o.Output)

	var rcFiles []string
	for _, s := range o.Sources {
		if strings.EqualFold(filepath.Ext(s), ".rc") {
			rcFiles = append(rcFiles, s)
		} else {
			args = append(args, s)
		}
	}
	if rc := resourceScript(o); rc != "" {
		p := filepath.Join(work, "exeforge.rc")
		if err := os.WriteFile(p, []byte(rc), 0o644); err != nil {
			return "", err
		}
		rcFiles = append(rcFiles, p)
	}
	for i, rc := range rcFiles {
		if tc.Kind == "zig" {
			args = append(args, rc) // zig cc compiles .rc files itself
			continue
		}
		if tc.Windres == "" {
			o.log("warning: windres not found, skipping resources (%s)", rc)
			continue
		}
		obj := filepath.Join(work, fmt.Sprintf("res%d.o", i))
		if err := run(o, tc.Windres, []string{"-O", "coff", "-i", rc, "-o", obj}, nil); err != nil {
			return "", fmt.Errorf("resource compile failed: %w", err)
		}
		args = append(args, obj)
	}
	for _, l := range o.Libs {
		args = append(args, "-l"+strings.TrimPrefix(l, "-l"))
	}
	var env []string
	if tc.Kind == "zig" {
		env = append(env, "ZIG_GLOBAL_CACHE_DIR="+filepath.Join(DataDir(), "zig-cache"))
	}
	if err := run(o, cc, args, env); err != nil {
		return "", err
	}
	return tc.Version, nil
}

func buildGo(o *Options, work string) (string, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return "", errors.New("Go is not installed (https://go.dev/dl)")
	}
	if o.Icon != "" || o.ProductName != "" {
		o.log("note: icons/version info are not embedded for Go builds (use a .syso tool like go-winres)")
	}
	ld := "-s -w"
	if o.Debug {
		ld = ""
	}
	if o.GUI {
		ld = strings.TrimSpace(ld + " -H windowsgui")
	}
	args := []string{"build", "-trimpath", "-o", o.Output}
	if ld != "" {
		args = append(args, "-ldflags", ld)
	}
	args = append(args, o.ExtraFlags...)
	dir := ""
	if st, _ := os.Stat(o.Sources[0]); st != nil && st.IsDir() {
		dir = o.Sources[0]
		args = append(args, ".")
	} else {
		// Single files: build outside any module.
		for _, s := range o.Sources {
			abs, _ := filepath.Abs(s)
			args = append(args, abs)
		}
	}
	env := []string{"GOOS=windows", "GOARCH=" + o.Arch.goArch(), "CGO_ENABLED=0"}
	if dir == "" {
		env = append(env, "GO111MODULE=off", "GOPATH="+filepath.Join(work, "gopath"),
			"GOCACHE="+filepath.Join(DataDir(), "go-cache"))
	}
	if err := runIn(o, dir, goBin, args, env); err != nil {
		return "", err
	}
	v, _ := exec.Command(goBin, "version").Output()
	return firstLine(v), nil
}

func buildRust(o *Options) (string, error) {
	rustc, err := exec.LookPath("rustc")
	if err != nil {
		return "", errors.New("Rust is not installed (https://rustup.rs)")
	}
	if len(o.Sources) != 1 {
		return "", errors.New("Rust builds take exactly one .rs file (the crate root)")
	}
	target := o.Arch.rustTarget()
	args := []string{"--target", target, "-o", o.Output, "--edition", "2021"}
	if o.Debug {
		args = append(args, "-g")
	} else {
		args = append(args, "-C", "opt-level=3", "-C", "strip=symbols")
	}
	if o.GUI {
		args = append(args, "-C", "link-arg=-mwindows")
	}
	if tc := FindMinGW(o.Arch); tc != nil {
		args = append(args, "-C", "linker="+tc.CC)
	}
	args = append(args, o.ExtraFlags...)
	args = append(args, o.Sources[0])
	if err := run(o, rustc, args, nil); err != nil {
		if strings.Contains(err.Error(), "can't find crate for `std`") {
			return "", fmt.Errorf("%w\nhint: rustup target add %s", err, target)
		}
		return "", err
	}
	v, _ := exec.Command(rustc, "--version").Output()
	return firstLine(v), nil
}

var versionRe = regexp.MustCompile(`^\d+(\.\d+){0,3}$`)

func resourceScript(o *Options) string {
	if o.Icon == "" && o.ProductName == "" && o.Version == "" {
		return ""
	}
	var b strings.Builder
	if o.Icon != "" {
		abs, _ := filepath.Abs(o.Icon)
		fmt.Fprintf(&b, "1 ICON \"%s\"\n", rcEscape(filepath.ToSlash(abs)))
	}
	if o.ProductName != "" || o.Version != "" {
		v := o.Version
		if !versionRe.MatchString(v) {
			v = "1.0.0"
		}
		parts := strings.Split(v, ".")
		for len(parts) < 4 {
			parts = append(parts, "0")
		}
		comma := strings.Join(parts, ",")
		name := o.ProductName
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(o.Output), ".exe")
		}
		name = rcEscape(name)
		fmt.Fprintf(&b, `1 VERSIONINFO
FILEVERSION %[1]s
PRODUCTVERSION %[1]s
FILEOS 0x40004
FILETYPE 0x1
BEGIN
  BLOCK "StringFileInfo"
  BEGIN
    BLOCK "040904B0"
    BEGIN
      VALUE "FileDescription", "%[2]s"
      VALUE "ProductName", "%[2]s"
      VALUE "FileVersion", "%[3]s"
      VALUE "ProductVersion", "%[3]s"
      VALUE "OriginalFilename", "%[4]s"
    END
  END
  BLOCK "VarFileInfo"
  BEGIN
    VALUE "Translation", 0x409, 1200
  END
END
`, comma, name, v, rcEscape(filepath.Base(o.Output)))
	}
	return b.String()
}

func rcEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `""`)
}

func run(o *Options, bin string, args, env []string) error { return runIn(o, "", bin, args, env) }

func runIn(o *Options, dir, bin string, args, env []string) error {
	o.log("$ %s %s", filepath.Base(bin), quoteArgs(args))
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if out := strings.TrimRight(buf.String(), "\n"); out != "" {
		o.log("%s", out)
	}
	if err != nil {
		return fmt.Errorf("%s failed: %v", filepath.Base(bin), err)
	}
	return nil
}

func quoteArgs(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'") {
			q[i] = fmt.Sprintf("%q", a)
		} else {
			q[i] = a
		}
	}
	return strings.Join(q, " ")
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
