// Package gui serves ExeForge's browser-based interface on localhost.
package gui

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/elloexe/compiler/internal/forge"
)

//go:embed web/index.html
var webFS embed.FS

type Server struct {
	token   string
	addr    string
	mu      sync.Mutex
	builds  map[string]string // id -> exe path
	version string
	quit    chan struct{}
}

type Config struct {
	Port      int
	NoBrowser bool
	Version   string
}

// DefaultOutDir is where GUI builds are saved.
func DefaultOutDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "ExeForge")
	}
	return filepath.Join(home, "ExeForge")
}

// Run starts the GUI server and blocks until it is closed.
func Run(cfg Config) error {
	tok := make([]byte, 16)
	rand.Read(tok)
	s := &Server{token: hex.EncodeToString(tok), builds: map[string]string{}, version: cfg.Version, quit: make(chan struct{})}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/api/status", s.api(s.status))
	mux.HandleFunc("/api/setup", s.api(s.setup))
	mux.HandleFunc("/api/build", s.api(s.build))
	mux.HandleFunc("/api/download", s.api(s.download))
	mux.HandleFunc("/api/run", s.api(s.run))
	mux.HandleFunc("/api/open-folder", s.api(s.openFolder))
	mux.HandleFunc("/api/quit", s.api(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true})
		go func() { time.Sleep(200 * time.Millisecond); close(s.quit) }()
	}))
	srv := &http.Server{Handler: s.hostCheck(mux)}

	url := fmt.Sprintf("http://%s/?t=%s", s.addr, s.token)
	fmt.Println("ExeForge GUI is running at:")
	fmt.Println("  " + url)
	fmt.Println("Press Ctrl+C (or click Quit in the GUI) to stop.")
	if !cfg.NoBrowser {
		go openBrowser(url)
	}
	go func() {
		<-s.quit
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// hostCheck blocks DNS-rebinding attacks: only accept our own host.
func (s *Server) hostCheck(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := net.SplitHostPort(s.addr)
		if r.Host != s.addr && r.Host != "localhost:"+port {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) api(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := r.Header.Get("X-Token")
		if t == "" {
			t = r.URL.Query().Get("t")
		}
		if t != s.token {
			http.Error(w, "bad token", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Token") != s.token {
			http.Error(w, "token header required", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, _ := webFS.ReadFile("web/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	tc, err := forge.FindToolchain("auto", forge.ArchX64)
	ready, detail := err == nil, ""
	if tc != nil {
		detail = tc.Version
	}
	writeJSON(w, map[string]any{
		"version":   s.version,
		"platform":  runtime.GOOS + "/" + runtime.GOARCH,
		"tools":     forge.Status(),
		"templates": forge.Templates,
		"outDir":    DefaultOutDir(),
		"ccReady":   ready,
		"ccDetail":  detail,
	})
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fl, _ := w.(http.Flusher)
	logf := func(l string) {
		fmt.Fprintln(w, l)
		if fl != nil {
			fl.Flush()
		}
	}
	if _, err := forge.InstallZig(logf); err != nil {
		logf("ERROR: " + err.Error())
		return
	}
	logf("DONE")
}

type buildReq struct {
	Arch        string `json:"arch"`
	GUI         bool   `json:"gui"`
	Debug       bool   `json:"debug"`
	Toolchain   string `json:"toolchain"`
	Libs        string `json:"libs"`
	Defines     string `json:"defines"`
	Flags       string `json:"flags"`
	OutName     string `json:"outName"`
	OutDir      string `json:"outDir"`
	ProductName string `json:"productName"`
	Version     string `json:"version"`
	Code        string `json:"code"`
	Filename    string `json:"filename"`
	Paths       string `json:"paths"` // newline-separated local paths
}

func safeName(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, `\`, "/"))
	if n == "." || n == "/" || n == "" || strings.HasPrefix(n, ".") {
		return ""
	}
	return n
}

func (s *Server) build(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var logBuf strings.Builder
	logf := func(l string) { logBuf.WriteString(l + "\n") }
	fail := func(err error) {
		logf("✘ " + err.Error())
		writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "log": logBuf.String()})
	}
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		fail(err)
		return
	}
	var req buildReq
	if err := json.Unmarshal([]byte(r.FormValue("options")), &req); err != nil {
		fail(fmt.Errorf("bad options: %v", err))
		return
	}
	arch, err := forge.ParseArch(req.Arch)
	if err != nil {
		fail(err)
		return
	}
	work, err := os.MkdirTemp("", "exeforge-gui-")
	if err != nil {
		fail(err)
		return
	}
	defer os.RemoveAll(work)

	var sources []string
	for _, fh := range r.MultipartForm.File["files"] {
		name := safeName(fh.Filename)
		if name == "" {
			continue
		}
		if err := saveUpload(fh.Open, filepath.Join(work, name)); err != nil {
			fail(err)
			return
		}
		if !isHeaderOrAsset(name) {
			sources = append(sources, filepath.Join(work, name))
		}
	}
	if strings.TrimSpace(req.Code) != "" {
		name := safeName(req.Filename)
		if name == "" {
			name = "main.c"
		}
		p := filepath.Join(work, name)
		if err := os.WriteFile(p, []byte(req.Code), 0o644); err != nil {
			fail(err)
			return
		}
		sources = append(sources, p)
	}
	for _, p := range strings.Split(req.Paths, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			sources = append(sources, p)
		}
	}
	if len(sources) == 0 {
		fail(fmt.Errorf("nothing to build: write some code, add files, or enter paths"))
		return
	}
	icon := ""
	if fhs := r.MultipartForm.File["icon"]; len(fhs) > 0 {
		icon = filepath.Join(work, "app.ico")
		if err := saveUpload(fhs[0].Open, icon); err != nil {
			fail(err)
			return
		}
	}
	outDir := strings.TrimSpace(req.OutDir)
	if outDir == "" {
		outDir = DefaultOutDir()
	}
	outName := safeName(strings.TrimSpace(req.OutName))
	if outName == "" {
		first := sources[0]
		outName = strings.TrimSuffix(filepath.Base(first), filepath.Ext(first))
	}
	if !strings.HasSuffix(strings.ToLower(outName), ".exe") {
		outName += ".exe"
	}
	opts := forge.Options{
		Sources: sources, Output: filepath.Join(outDir, outName), Arch: arch,
		GUI: req.GUI, Debug: req.Debug, Toolchain: req.Toolchain, Icon: icon,
		ProductName: req.ProductName, Version: req.Version,
		Libs: strings.Fields(strings.ReplaceAll(req.Libs, ",", " ")),
		Defines: strings.Fields(req.Defines), ExtraFlags: strings.Fields(req.Flags),
		Includes: []string{work}, Log: logf,
	}
	res, err := forge.Build(opts)
	if err != nil {
		fail(err)
		return
	}
	info, _ := forge.InspectPE(res.Output)
	idb := make([]byte, 8)
	rand.Read(idb)
	id := hex.EncodeToString(idb)
	s.mu.Lock()
	s.builds[id] = res.Output
	s.mu.Unlock()
	writeJSON(w, map[string]any{
		"ok": true, "log": logBuf.String(), "id": id, "output": res.Output,
		"name": filepath.Base(res.Output), "size": res.Size, "info": info,
		"compiler": res.Compiler, "ms": res.Duration.Milliseconds(),
	})
}

func isHeaderOrAsset(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".h", ".hpp", ".hh", ".hxx", ".inc", ".ico", ".bmp", ".png", ".manifest", ".txt":
		return true
	}
	return false
}

type multipartFile = multipart.File

func saveUpload(open func() (multipartFile, error), dst string) error {
	src, err := open()
	if err != nil {
		return err
	}
	defer src.Close()
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, src)
	return err
}

func (s *Server) lookup(r *http.Request) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.builds[r.URL.Query().Get("id")]
	return p, ok
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	p, ok := s.lookup(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(p)))
	w.Header().Set("Content-Type", "application/vnd.microsoft.portable-executable")
	http.ServeFile(w, r, p)
}

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	p, ok := s.lookup(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	out, err := forge.RunExe(p, nil, 20*time.Second)
	res := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		res["error"] = err.Error()
	}
	writeJSON(w, res)
}

func (s *Server) openFolder(w http.ResponseWriter, r *http.Request) {
	dir := DefaultOutDir()
	if p, ok := s.lookup(r); ok {
		dir = filepath.Dir(p)
	}
	os.MkdirAll(dir, 0o755)
	err := openPath(dir)
	writeJSON(w, map[string]any{"ok": err == nil, "dir": dir})
}

func openBrowser(url string) { openPath(url) }

func openPath(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}
