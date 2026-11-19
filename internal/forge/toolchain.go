package forge

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Arch is a Windows target architecture.
type Arch string

const (
	ArchX64   Arch = "x64"
	ArchX86   Arch = "x86"
	ArchARM64 Arch = "arm64"
)

func ParseArch(s string) (Arch, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "x64", "amd64", "x86_64", "64":
		return ArchX64, nil
	case "x86", "i386", "i686", "386", "32":
		return ArchX86, nil
	case "arm64", "aarch64":
		return ArchARM64, nil
	}
	return "", fmt.Errorf("unknown arch %q (use x64, x86 or arm64)", s)
}

func (a Arch) mingwPrefix() string {
	switch a {
	case ArchX86:
		return "i686-w64-mingw32-"
	case ArchARM64:
		return "aarch64-w64-mingw32-"
	}
	return "x86_64-w64-mingw32-"
}

func (a Arch) zigTarget() string {
	switch a {
	case ArchX86:
		return "x86-windows-gnu"
	case ArchARM64:
		return "aarch64-windows-gnu"
	}
	return "x86_64-windows-gnu"
}

func (a Arch) goArch() string {
	switch a {
	case ArchX86:
		return "386"
	case ArchARM64:
		return "arm64"
	}
	return "amd64"
}

func (a Arch) rustTarget() string {
	switch a {
	case ArchX86:
		return "i686-pc-windows-gnu"
	case ArchARM64:
		return "aarch64-pc-windows-gnullvm"
	}
	return "x86_64-pc-windows-gnu"
}

// Toolchain describes one way of producing a Windows executable from C/C++.
type Toolchain struct {
	Kind    string // "mingw" or "zig"
	CC      string // C compiler command
	CXX     string // C++ compiler command
	Windres string // resource compiler (mingw only; zig compiles .rc itself)
	Version string
}

// DataDir is where ExeForge keeps its downloaded compiler.
func DataDir() string {
	if d := os.Getenv("EXEFORGE_HOME"); d != "" {
		return d
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "exeforge")
	}
	return filepath.Join(os.TempDir(), "exeforge")
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// FindMinGW looks for a gcc that targets Windows for the given arch.
func FindMinGW(arch Arch) *Toolchain {
	p := arch.mingwPrefix()
	candidates := []string{p + "gcc"}
	// On Windows, MSYS2/MinGW installs plain "gcc" that targets Windows.
	if runtime.GOOS == "windows" {
		candidates = append(candidates, "gcc")
	}
	for _, cc := range candidates {
		path, err := exec.LookPath(cc)
		if err != nil {
			continue
		}
		out, err := exec.Command(path, "-dumpmachine").Output()
		if err != nil {
			continue
		}
		machine := strings.TrimSpace(string(out))
		if !strings.Contains(machine, "mingw") && !strings.Contains(machine, "windows") {
			continue
		}
		if !archMatchesMachine(arch, machine) {
			continue
		}
		cxxName := strings.TrimSuffix(cc, "gcc") + "g++"
		cxx, _ := exec.LookPath(cxxName)
		wr, _ := exec.LookPath(strings.TrimSuffix(cc, "gcc") + "windres")
		if wr == "" {
			wr, _ = exec.LookPath("windres")
		}
		ver, _ := exec.Command(path, "-dumpfullversion").Output()
		return &Toolchain{Kind: "mingw", CC: path, CXX: cxx, Windres: wr,
			Version: "gcc " + strings.TrimSpace(string(ver)) + " (" + machine + ")"}
	}
	return nil
}

func archMatchesMachine(arch Arch, machine string) bool {
	switch arch {
	case ArchX86:
		return strings.HasPrefix(machine, "i686") || strings.HasPrefix(machine, "i386")
	case ArchARM64:
		return strings.HasPrefix(machine, "aarch64")
	}
	return strings.HasPrefix(machine, "x86_64")
}

// ManagedZigPath is where `exeforge setup` installs Zig.
func ManagedZigPath() string {
	return filepath.Join(DataDir(), "zig-"+zigVersion, "zig"+exeSuffix())
}

// FindZig returns a Zig toolchain (system or ExeForge-managed).
func FindZig() *Toolchain {
	var path string
	if p := ManagedZigPath(); fileExists(p) {
		path = p
	} else if p, err := exec.LookPath("zig"); err == nil {
		path = p
	}
	if path == "" {
		return nil
	}
	ver, err := exec.Command(path, "version").Output()
	if err != nil {
		return nil
	}
	return &Toolchain{Kind: "zig", CC: path, CXX: path, Version: "zig " + firstLine(ver)}
}

// FindToolchain picks a C/C++ toolchain. pref is "auto", "mingw" or "zig".
func FindToolchain(pref string, arch Arch) (*Toolchain, error) {
	switch pref {
	case "mingw":
		if t := FindMinGW(arch); t != nil {
			return t, nil
		}
		return nil, fmt.Errorf("no MinGW-w64 compiler for %s found (install mingw-w64, or use --toolchain zig)", arch)
	case "zig":
		if t := FindZig(); t != nil {
			return t, nil
		}
		return nil, errors.New("zig not found: run `exeforge setup` to download it")
	}
	if t := FindMinGW(arch); t != nil {
		return t, nil
	}
	if t := FindZig(); t != nil {
		return t, nil
	}
	return nil, errors.New("no Windows C/C++ compiler found.\n" +
		"  Run `exeforge setup` (or click \"Install compiler\" in the GUI) to download Zig automatically,\n" +
		"  or install MinGW-w64 (Debian/Ubuntu: sudo apt install mingw-w64, Fedora: sudo dnf install mingw64-gcc)")
}

// ToolInfo is a human-readable status line for one tool.
type ToolInfo struct {
	Name   string `json:"name"`
	Found  bool   `json:"found"`
	Detail string `json:"detail"`
	For    string `json:"for"`
}

// Status reports everything ExeForge can use on this machine.
func Status() []ToolInfo {
	var out []ToolInfo
	for _, a := range []Arch{ArchX64, ArchX86, ArchARM64} {
		ti := ToolInfo{Name: "MinGW-w64 (" + string(a) + ")", For: "C / C++"}
		if t := FindMinGW(a); t != nil {
			ti.Found, ti.Detail = true, t.Version
		} else {
			ti.Detail = "not installed"
		}
		out = append(out, ti)
	}
	zi := ToolInfo{Name: "Zig", For: "C / C++ (x64, x86, arm64)"}
	if t := FindZig(); t != nil {
		zi.Found, zi.Detail = true, t.Version+" — "+t.CC
	} else {
		zi.Detail = "not installed (exeforge setup downloads it)"
	}
	out = append(out, zi)

	gi := ToolInfo{Name: "Go", For: "Go"}
	if p, err := exec.LookPath("go"); err == nil {
		v, _ := exec.Command(p, "version").Output()
		gi.Found, gi.Detail = true, firstLine(v)
	} else {
		gi.Detail = "not installed"
	}
	out = append(out, gi)

	ri := ToolInfo{Name: "Rust", For: "Rust"}
	if p, err := exec.LookPath("rustc"); err == nil {
		v, _ := exec.Command(p, "--version").Output()
		ri.Found, ri.Detail = true, firstLine(v)
		if tg := rustInstalledTargets(); len(tg) > 0 {
			ri.Detail += " — windows targets: " + strings.Join(tg, ", ")
		} else {
			ri.Detail += " — no windows target (rustup target add x86_64-pc-windows-gnu)"
		}
	} else {
		ri.Detail = "not installed"
	}
	out = append(out, ri)

	wi := ToolInfo{Name: "Wine", For: "running .exe files on Linux"}
	if runtime.GOOS == "windows" {
		wi.Name, wi.Found, wi.Detail = "Native Windows", true, "exe files run directly"
	} else if p := FindWine(); p != "" {
		wi.Found, wi.Detail = true, p
	} else {
		wi.Detail = "not installed (optional)"
	}
	out = append(out, wi)
	return out
}

func rustInstalledTargets() []string {
	out, err := exec.Command("rustup", "target", "list", "--installed").Output()
	if err != nil {
		return nil
	}
	var r []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, "windows") {
			r = append(r, strings.TrimSpace(l))
		}
	}
	return r
}

// FindWine returns a wine binary path, or "".
func FindWine() string {
	for _, n := range []string{"wine", "wine64"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	for _, p := range []string{"/usr/lib/wine/wine64", "/usr/lib/wine/wine", "/opt/wine-stable/bin/wine"} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// ---- Zig download -------------------------------------------------------

const zigVersion = "0.13.0"

type zigDist struct{ url, sha string }

var zigDists = map[string]zigDist{
	"linux/amd64":   {"https://ziglang.org/download/0.13.0/zig-linux-x86_64-0.13.0.tar.xz", "d45312e61ebcc48032b77bc4cf7fd6915c11fa16e4aad116b66c9468211230ea"},
	"linux/arm64":   {"https://ziglang.org/download/0.13.0/zig-linux-aarch64-0.13.0.tar.xz", "041ac42323837eb5624068acd8b00cd5777dac4cf91179e8dad7a7e90dd0c556"},
	"windows/amd64": {"https://ziglang.org/download/0.13.0/zig-windows-x86_64-0.13.0.zip", "d859994725ef9402381e557c60bb57497215682e355204d754ee3df75ee3c158"},
	"windows/arm64": {"https://ziglang.org/download/0.13.0/zig-windows-aarch64-0.13.0.zip", "95ff88427af7ba2b4f312f45d2377ce7a033e5e3c620c8caaa396a9aba20efda"},
	"darwin/amd64":  {"https://ziglang.org/download/0.13.0/zig-macos-x86_64-0.13.0.tar.xz", "8b06ed1091b2269b700b3b07f8e3be3b833000841bae5aa6a09b1a8b4773effd"},
	"darwin/arm64":  {"https://ziglang.org/download/0.13.0/zig-macos-aarch64-0.13.0.tar.xz", "46fae219656545dfaf4dce12fb4e8685cec5b51d721beee9389ab4194d43394c"},
}

type progressWriter struct {
	log        func(string)
	total, cur int64
	lastPct    int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.cur += int64(len(b))
	if p.total > 0 {
		pct := p.cur * 100 / p.total
		if pct/10 != p.lastPct/10 {
			p.lastPct = pct
			p.log(fmt.Sprintf("  downloaded %d%% (%d MB)", pct, p.cur>>20))
		}
	}
	return len(b), nil
}

// InstallZig downloads and unpacks Zig into DataDir(). log receives progress.
func InstallZig(log func(string)) (string, error) {
	if p := ManagedZigPath(); fileExists(p) {
		log("Zig " + zigVersion + " is already installed at " + p)
		return p, nil
	}
	key := runtime.GOOS + "/" + runtime.GOARCH
	d, ok := zigDists[key]
	if !ok {
		return "", fmt.Errorf("no Zig download known for %s; install zig manually and put it on PATH", key)
	}
	if err := os.MkdirAll(DataDir(), 0o755); err != nil {
		return "", err
	}
	log("Downloading " + d.url)
	resp, err := http.Get(d.url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}
	archive := filepath.Join(DataDir(), filepath.Base(d.url)+".part")
	f, err := os.Create(archive)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	pw := &progressWriter{log: log, total: resp.ContentLength}
	if _, err := io.Copy(io.MultiWriter(f, h, pw), resp.Body); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	defer os.Remove(archive)
	if got := hex.EncodeToString(h.Sum(nil)); got != d.sha {
		return "", fmt.Errorf("checksum mismatch: got %s, want %s", got, d.sha)
	}
	log("Checksum OK, extracting…")

	tmp, err := os.MkdirTemp(DataDir(), "extract-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if strings.HasSuffix(d.url, ".zip") {
		err = unzip(archive, tmp)
	} else {
		var out []byte
		out, err = exec.Command("tar", "-xJf", archive, "-C", tmp).CombinedOutput()
		if err != nil {
			err = fmt.Errorf("tar failed (is xz installed?): %v\n%s", err, out)
		}
	}
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 1 {
		return "", errors.New("unexpected archive layout")
	}
	dest := filepath.Dir(ManagedZigPath())
	os.RemoveAll(dest)
	if err := os.Rename(filepath.Join(tmp, entries[0].Name()), dest); err != nil {
		return "", err
	}
	log("Installed Zig " + zigVersion + " to " + dest)
	return ManagedZigPath(), nil
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	root := filepath.Clean(dest) + string(os.PathSeparator)
	for _, f := range r.File {
		p := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(p, root) {
			return fmt.Errorf("bad path in zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(p, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()|0o600)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(w, rc)
		rc.Close()
		w.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
