package forge

// Template is a starter program the CLI (`exeforge new`) and the GUI offer.
type Template struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Filename string `json:"filename"`
	GUI      bool   `json:"gui"`
	Libs     string `json:"libs"`
	Code     string `json:"code"`
}

var Templates = []Template{
	{ID: "console-c", Name: "Console app (C)", Filename: "hello.c", Code: `#include <stdio.h>

int main(int argc, char **argv) {
    printf("Hello from a Windows exe built with ExeForge!\n");
    for (int i = 1; i < argc; i++)
        printf("arg %d: %s\n", i, argv[i]);
    return 0;
}
`},
	{ID: "console-cpp", Name: "Console app (C++)", Filename: "hello.cpp", Code: `#include <iostream>
#include <string>
#include <vector>

int main() {
    std::vector<std::string> langs = {"C", "C++", "Go", "Rust"};
    std::cout << "Hello from C++ on Windows!\n";
    for (const auto &l : langs)
        std::cout << "  ExeForge can build: " << l << "\n";
    return 0;
}
`},
	{ID: "messagebox", Name: "Message box (Win32 GUI)", Filename: "msgbox.c", GUI: true, Libs: "user32", Code: `#include <windows.h>

int WINAPI WinMain(HINSTANCE hInst, HINSTANCE hPrev, LPSTR cmd, int show) {
    MessageBoxA(NULL, "Hello from ExeForge!", "ExeForge", MB_OK | MB_ICONINFORMATION);
    return 0;
}
`},
	{ID: "window", Name: "Window with button (Win32 GUI)", Filename: "window.c", GUI: true, Libs: "user32 gdi32", Code: `#include <windows.h>

#define ID_BUTTON 1

static LRESULT CALLBACK WndProc(HWND hwnd, UINT msg, WPARAM wp, LPARAM lp) {
    switch (msg) {
    case WM_CREATE:
        CreateWindowA("BUTTON", "Click me", WS_CHILD | WS_VISIBLE | BS_PUSHBUTTON,
                      110, 70, 160, 40, hwnd, (HMENU)ID_BUTTON, NULL, NULL);
        return 0;
    case WM_COMMAND:
        if (LOWORD(wp) == ID_BUTTON)
            MessageBoxA(hwnd, "Button clicked!", "ExeForge", MB_OK);
        return 0;
    case WM_DESTROY:
        PostQuitMessage(0);
        return 0;
    }
    return DefWindowProcA(hwnd, msg, wp, lp);
}

int WINAPI WinMain(HINSTANCE hInst, HINSTANCE hPrev, LPSTR cmd, int show) {
    WNDCLASSA wc = {0};
    wc.lpfnWndProc = WndProc;
    wc.hInstance = hInst;
    wc.hCursor = LoadCursor(NULL, IDC_ARROW);
    wc.hbrBackground = (HBRUSH)(COLOR_WINDOW + 1);
    wc.lpszClassName = "ExeForgeWindow";
    RegisterClassA(&wc);

    HWND hwnd = CreateWindowA("ExeForgeWindow", "Made with ExeForge",
                              WS_OVERLAPPEDWINDOW, CW_USEDEFAULT, CW_USEDEFAULT,
                              400, 220, NULL, NULL, hInst, NULL);
    ShowWindow(hwnd, show);

    MSG m;
    while (GetMessageA(&m, NULL, 0, 0) > 0) {
        TranslateMessage(&m);
        DispatchMessageA(&m);
    }
    return 0;
}
`},
	{ID: "go", Name: "Console app (Go)", Filename: "hello.go", Code: `package main

import (
	"fmt"
	"runtime"
)

func main() {
	fmt.Printf("Hello from Go on %s/%s!\n", runtime.GOOS, runtime.GOARCH)
}
`},
}

func TemplateByID(id string) *Template {
	for i := range Templates {
		if Templates[i].ID == id {
			return &Templates[i]
		}
	}
	return nil
}
