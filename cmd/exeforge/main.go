// ExeForge: build Windows .exe files on Linux (and on Windows).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/elloexe/compiler/internal/forge"
	"github.com/elloexe/compiler/internal/gui"
)

var version = "1.0.0"

const usage = `ExeForge %s — build Windows .exe files from C, C++, Go and Rust

Usage:
  exeforge build [options] <source files...>   compile to a Windows .exe
  exeforge gui [--port N] [--no-browser]       open the graphical interface
  exeforge new <template> [file]               create a starter program
  exeforge run <file.exe> [args...]            run an exe (via Wine on Linux)
  exeforge info <file.exe>                     show arch, subsystem and DLL imports
  exeforge setup                               download the Zig cross-compiler
  exeforge doctor                              show which compilers are available
  exeforge install                             add exeforge to PATH + app menu (Linux)
  exeforge version

Build options:
  -o, --output FILE     output path (default: <first source>.exe)
  -a, --arch ARCH       x64 (default), x86, arm64
  -w, --gui             windowed app, no console window (WinMain / -mwindows)
  -l LIB                link a Windows library, e.g. -l user32 -l gdi32 (repeatable)
  -D NAME[=VAL]         preprocessor define (repeatable)
  -I DIR                include directory (repeatable)
      --icon FILE.ico   embed an icon
      --name NAME       embed product name (version info)
      --version X.Y.Z   embed version number
  -g, --debug           debug build (default: optimised and stripped)
      --toolchain T     auto (default), mingw, zig
      --run             run the exe after building (Wine on Linux)
  --                    pass everything after this straight to the compiler

Templates for "new": %s

Examples:
  exeforge build hello.c
  exeforge build -w -l user32 -o app.exe main.c gui.c --icon app.ico
  exeforge build -a x86 tool.cpp
  exeforge build ./mygoapp
`

func templateIDs() string {
	var ids []string
	for _, t := range forge.Templates {
		ids = append(ids, t.ID)
	}
	return strings.Join(ids, ", ")
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		// Double-clicked (Windows) or started from a desktop launcher: open the GUI.
		if runtime.GOOS == "windows" || !isTerminal() {
			check(gui.Run(gui.Config{Version: version}))
			return
		}
		fmt.Printf(usage, version, templateIDs())
		fmt.Println("Tip: run `exeforge gui` for the graphical interface.")
		return
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "build", "b":
		cmdBuild(rest)
	case "gui", "ui":
		cmdGUI(rest)
	case "new":
		cmdNew(rest)
	case "run":
		cmdRun(rest)
	case "info":
		cmdInfo(rest)
	case "setup":
		_, err := forge.InstallZig(func(s string) { fmt.Println(s) })
		check(err)
	case "doctor", "status":
		cmdDoctor()
	case "install":
		cmdInstall()
	case "version", "--version", "-v":
		fmt.Println("exeforge", version, runtime.GOOS+"/"+runtime.GOARCH)
	case "help", "-h", "--help":
		fmt.Printf(usage, version, templateIDs())
	default:
		// Allow `exeforge hello.c` as shorthand for build.
		if _, err := os.Stat(cmd); err == nil {
			cmdBuild(args)
			return
		}
		fmt.Fprintf(os.Stderr, "unknown command %q (try `exeforge help`)\n", cmd)
		os.Exit(2)
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func isTerminal() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func cmdBuild(args []string) {
	o := forge.Options{Log: func(s string) { fmt.Println(s) }}
	runAfter := false
	need := func(i int, flag string) string {
		if i+1 >= len(args) {
			check(fmt.Errorf("%s needs a value", flag))
		}
		return args[i+1]
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		// Support -lfoo / -DX / -Idir and --flag=value forms.
		if k, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(a, "--") {
			args = append(args[:i], append([]string{k, v}, args[i+1:]...)...)
			a = k
		}
		switch {
		case a == "--":
			o.ExtraFlags = append(o.ExtraFlags, args[i+1:]...)
			i = len(args)
		case a == "-o" || a == "--output":
			o.Output = need(i, a)
			i++
		case a == "-a" || a == "--arch":
			arch, err := forge.ParseArch(need(i, a))
			check(err)
			o.Arch = arch
			i++
		case a == "-w" || a == "--gui" || a == "--windows" || a == "-mwindows":
			o.GUI = true
		case a == "-g" || a == "--debug":
			o.Debug = true
		case a == "--icon":
			o.Icon = need(i, a)
			i++
		case a == "--name":
			o.ProductName = need(i, a)
			i++
		case a == "--version":
			o.Version = need(i, a)
			i++
		case a == "--toolchain":
			o.Toolchain = need(i, a)
			i++
		case a == "--run":
			runAfter = true
		case a == "-l" || a == "-D" || a == "-I":
			v := need(i, a)
			i++
			addPrefixed(&o, a, v)
		case len(a) > 2 && (strings.HasPrefix(a, "-l") || strings.HasPrefix(a, "-D") || strings.HasPrefix(a, "-I")):
			addPrefixed(&o, a[:2], a[2:])
		case strings.HasPrefix(a, "-"):
			check(fmt.Errorf("unknown option %s (put raw compiler flags after --)", a))
		default:
			o.Sources = append(o.Sources, a)
		}
	}
	res, err := forge.Build(o)
	check(err)
	if runAfter {
		fmt.Println("--- running", filepath.Base(res.Output), "---")
		out, err := forge.RunExe(res.Output, nil, 60*time.Second)
		fmt.Print(out)
		check(err)
	}
}

func addPrefixed(o *forge.Options, flag, v string) {
	switch flag {
	case "-l":
		o.Libs = append(o.Libs, v)
	case "-D":
		o.Defines = append(o.Defines, v)
	case "-I":
		o.Includes = append(o.Includes, v)
	}
}

func cmdGUI(args []string) {
	cfg := gui.Config{Version: version}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-browser":
			cfg.NoBrowser = true
		case "--port", "-p":
			if i+1 < len(args) {
				fmt.Sscan(args[i+1], &cfg.Port)
				i++
			}
		}
	}
	check(gui.Run(cfg))
}

func cmdNew(args []string) {
	if len(args) == 0 {
		fmt.Println("Templates:")
		for _, t := range forge.Templates {
			fmt.Printf("  %-12s %s (%s)\n", t.ID, t.Name, t.Filename)
		}
		return
	}
	t := forge.TemplateByID(args[0])
	if t == nil {
		check(fmt.Errorf("unknown template %q (available: %s)", args[0], templateIDs()))
	}
	name := t.Filename
	if len(args) > 1 {
		name = args[1]
	}
	if _, err := os.Stat(name); err == nil {
		check(fmt.Errorf("%s already exists", name))
	}
	check(os.WriteFile(name, []byte(t.Code), 0o644))
	fmt.Println("Created", name)
	cmd := "exeforge build " + name
	if t.GUI {
		cmd = "exeforge build -w"
		for _, l := range strings.Fields(t.Libs) {
			cmd += " -l " + l
		}
		cmd += " " + name
	}
	fmt.Println("Build it with:", cmd)
}

func cmdRun(args []string) {
	if len(args) == 0 {
		check(fmt.Errorf("usage: exeforge run <file.exe> [args...]"))
	}
	out, err := forge.RunExe(args[0], args[1:], 10*time.Minute)
	fmt.Print(out)
	check(err)
}

func cmdInfo(args []string) {
	if len(args) == 0 {
		check(fmt.Errorf("usage: exeforge info <file.exe>"))
	}
	for _, p := range args {
		info, err := forge.InspectPE(p)
		check(err)
		st, _ := os.Stat(p)
		fmt.Printf("%s\n  arch:      %s\n  subsystem: %s\n  size:      %d bytes\n  imports:   %s\n",
			p, info.Machine, info.Subsystem, st.Size(), strings.Join(info.DLLs, ", "))
	}
}

func cmdDoctor() {
	fmt.Printf("ExeForge %s on %s/%s\n\n", version, runtime.GOOS, runtime.GOARCH)
	for _, t := range forge.Status() {
		mark := "✘"
		if t.Found {
			mark = "✔"
		}
		fmt.Printf("  %s %-20s %s\n      for: %s\n", mark, t.Name, t.Detail, t.For)
	}
	if _, err := forge.FindToolchain("auto", forge.ArchX64); err != nil {
		fmt.Println("\nNo C/C++ compiler yet. Run: exeforge setup")
	} else {
		fmt.Println("\nReady to build. Try: exeforge new console-c && exeforge build hello.c")
	}
}

func cmdInstall() {
	if runtime.GOOS == "windows" {
		fmt.Println("On Windows just keep exeforge.exe anywhere and double-click it.")
		return
	}
	self, err := os.Executable()
	check(err)
	self, _ = filepath.EvalSymlinks(self)
	home, err := os.UserHomeDir()
	check(err)
	binDir := filepath.Join(home, ".local", "bin")
	check(os.MkdirAll(binDir, 0o755))
	dst := filepath.Join(binDir, "exeforge")
	if self != dst {
		data, err := os.ReadFile(self)
		check(err)
		tmp := dst + ".new"
		check(os.WriteFile(tmp, data, 0o755))
		check(os.Rename(tmp, dst))
	}
	fmt.Println("Installed", dst)
	if runtime.GOOS == "linux" {
		appDir := filepath.Join(home, ".local", "share", "applications")
		check(os.MkdirAll(appDir, 0o755))
		desktop := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=ExeForge
GenericName=Windows EXE Builder
Comment=Build Windows .exe files from C, C++, Go and Rust
Exec=%s gui
Icon=applications-development
Terminal=false
Categories=Development;
`, dst)
		p := filepath.Join(appDir, "exeforge.desktop")
		check(os.WriteFile(p, []byte(desktop), 0o644))
		fmt.Println("Added app menu entry", p)
	}
	if !strings.Contains(os.Getenv("PATH"), binDir) {
		fmt.Printf("Note: add %s to your PATH, e.g.  echo 'export PATH=\"$HOME/.local/bin:$PATH\"' >> ~/.bashrc\n", binDir)
	}
}
