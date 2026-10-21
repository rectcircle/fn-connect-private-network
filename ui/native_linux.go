//go:build linux

package main

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.1 ayatana-appindicator3-0.1
#include <stdlib.h>
void fn_init(char*);
void fn_dispose(void);
void fn_present_main(void);
void fn_tray(char*,char*,char*,int,int,int);
void fn_admin(char*);
*/
import "C"
import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"
)

//export fnGoAction
func fnGoAction(value *C.char) { action := C.GoString(value); go nativeAction(action) }

var linuxStatusIconPath = "/usr/share/fncpn/icons"
var linuxUIExecutable = "/usr/bin/fncpn-ui"

func nativeInit() {
	path := C.CString(linuxStatusIconPath)
	defer C.free(unsafe.Pointer(path))
	C.fn_init(path)
}
func nativeDispose()     { C.fn_dispose() }
func nativePresentMain() { C.fn_present_main() }
func nativeTray(state, title, action string, needsLogin, busy bool) {
	s, t, a := C.CString(state), C.CString(title), C.CString(action)
	defer C.free(unsafe.Pointer(s))
	defer C.free(unsafe.Pointer(t))
	defer C.free(unsafe.Pointer(a))
	var n, b, auto C.int
	if needsLogin {
		n = 1
	}
	if busy {
		b = 1
	}
	if nativeAutoStart() {
		auto = 1
	}
	C.fn_tray(s, t, a, n, b, auto)
}
func nativeAdmin(address string) {
	value := C.CString(address)
	defer C.free(unsafe.Pointer(value))
	C.fn_admin(value)
}
func nativeSymbols() map[string]string { return nil }
func autostartPath() string {
	directory := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(directory) {
		home, _ := os.UserHomeDir()
		directory = filepath.Join(home, ".config")
	}
	return filepath.Join(directory, "autostart/fncpn.desktop")
}

const autostartDesktop = "[Desktop Entry]\n# Managed by FnCPN\nType=Application\nName=FnCPN\nExec=%s --background\nIcon=fncpn\nTerminal=false\nX-GNOME-Autostart-enabled=true\n"

func nativeAutoStart() bool {
	data, err := os.ReadFile(autostartPath())
	return err == nil && strings.Contains(string(data), "# Managed by FnCPN") && !strings.Contains(string(data), "Hidden=true")
}
func nativeSetAutoStart(enabled bool) string {
	path := autostartPath()
	if old, err := os.ReadFile(path); err == nil && !strings.Contains(string(old), "# Managed by FnCPN") {
		return "autostart file is managed by another program"
	}
	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err.Error()
		}
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err.Error()
	}
	if err := os.WriteFile(path+".tmp", []byte(fmt.Sprintf(autostartDesktop, linuxUIExecutable)), 0600); err != nil {
		return err.Error()
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return err.Error()
	}
	return ""
}
func nativeLanguage() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := os.Getenv(name); value != "" {
			if strings.HasPrefix(strings.ToLower(value), "zh") {
				return "zh"
			}
			return "en"
		}
	}
	return "en"
}
func nativeLoginLaunch() bool {
	for _, arg := range os.Args[1:] {
		if arg == "--background" {
			return true
		}
	}
	return false
}
func nativeNotifyRoot() {
	_ = exec.Command("notify-send", "FnCPN", fmt.Sprint("Privileged service unavailable. Run fncpn-linux-setup.")).Start()
}
