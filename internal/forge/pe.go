package forge

import (
	"debug/pe"
	"fmt"
	"sort"
	"strings"
)

// PEInfo summarises a Windows executable.
type PEInfo struct {
	Machine   string   `json:"machine"`
	Subsystem string   `json:"subsystem"`
	Is64      bool     `json:"is64"`
	DLLs      []string `json:"dlls"`
	Sections  int      `json:"sections"`
}

// InspectPE parses a .exe/.dll and reports its architecture, subsystem and imports.
func InspectPE(path string) (*PEInfo, error) {
	f, err := pe.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info := &PEInfo{Sections: len(f.Sections)}
	switch f.Machine {
	case pe.IMAGE_FILE_MACHINE_AMD64:
		info.Machine = "x64"
	case pe.IMAGE_FILE_MACHINE_I386:
		info.Machine = "x86"
	case pe.IMAGE_FILE_MACHINE_ARM64:
		info.Machine = "arm64"
	default:
		info.Machine = fmt.Sprintf("machine 0x%x", f.Machine)
	}
	var sub uint16
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		sub, info.Is64 = h.Subsystem, true
	case *pe.OptionalHeader32:
		sub = h.Subsystem
	default:
		return nil, fmt.Errorf("no optional header (object file, not an executable?)")
	}
	switch sub {
	case pe.IMAGE_SUBSYSTEM_WINDOWS_GUI:
		info.Subsystem = "GUI"
	case pe.IMAGE_SUBSYSTEM_WINDOWS_CUI:
		info.Subsystem = "console"
	default:
		info.Subsystem = fmt.Sprintf("subsystem %d", sub)
	}
	// debug/pe's ImportedLibraries is a stub; derive DLL names from symbols ("Func:dll").
	syms, _ := f.ImportedSymbols()
	seen := map[string]bool{}
	for _, s := range syms {
		_, l, ok := strings.Cut(s, ":")
		if !ok {
			continue
		}
		l = strings.ToLower(l)
		if !seen[l] {
			seen[l] = true
			info.DLLs = append(info.DLLs, l)
		}
	}
	sort.Strings(info.DLLs)
	return info, nil
}
