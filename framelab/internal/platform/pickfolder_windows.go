//go:build windows

package platform

// PickFolder: the Windows folder picker (the same dialog Explorer uses), for Settings' Browse buttons.
// COM IFileOpenDialog with FOS_PICKFOLDERS, on its own locked OS thread, owned by Frame Lab's window
// when that window is in front.

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32pf                         = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstancePF          = ole32pf.NewProc("CoCreateInstance")
	shell32pf                       = windows.NewLazySystemDLL("shell32.dll")
	procSHCreateItemFromParsingName = shell32pf.NewProc("SHCreateItemFromParsingName")

	clsidFileOpenDialog = windows.GUID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE, Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidIFileOpenDialog  = windows.GUID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768, Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	iidIShellItem       = windows.GUID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE, Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
)

// ErrPickCancelled: the picker was closed without a folder.
var ErrPickCancelled = errors.New("cancelled")

const (
	fosPickFolders       = 0x20
	fosForceFileSystem   = 0x40
	fosPathMustExist     = 0x800
	sigdnFileSysPath     = 0x80058000
	hrCancelled          = 0x800704C7
	clsctxInprocServer   = 0x1
	vtRelease            = 2
	vtShow               = 3
	vtSetOptions         = 9
	vtGetOptions         = 10
	vtSetFolder          = 12
	vtSetTitle           = 17
	vtGetResult          = 20
	vtItemGetDisplayName = 5
)

func pfCall(obj uintptr, idx int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func PickFolder(title, start string) (string, error) {
	type res struct {
		p   string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		p, err := pickFolder(title, start)
		ch <- res{p, err}
	}()
	r := <-ch
	return r.p, r.err
}

func pickFolder(title, start string) (string, error) {
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err == nil || err == syscall.Errno(1) {
		defer windows.CoUninitialize()
	}
	var dlg uintptr
	hr, _, _ := procCoCreateInstancePF.Call(uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)), uintptr(unsafe.Pointer(&dlg)))
	if hr != 0 || dlg == 0 {
		return "", errors.New("the Windows folder picker could not be opened")
	}
	defer pfCall(dlg, vtRelease)

	var opts uint32
	pfCall(dlg, vtGetOptions, uintptr(unsafe.Pointer(&opts)))
	pfCall(dlg, vtSetOptions, uintptr(opts|fosPickFolders|fosForceFileSystem|fosPathMustExist))
	if title != "" {
		if t, err := windows.UTF16PtrFromString(title); err == nil {
			pfCall(dlg, vtSetTitle, uintptr(unsafe.Pointer(t)))
		}
	}
	if start != "" {
		if st, err := os.Stat(start); err == nil && st.IsDir() {
			if p, err := windows.UTF16PtrFromString(start); err == nil {
				var item uintptr
				if r, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&iidIShellItem)), uintptr(unsafe.Pointer(&item))); r == 0 && item != 0 {
					pfCall(dlg, vtSetFolder, item)
					pfCall(item, vtRelease)
				}
			}
		}
	}
	owner := windows.GetForegroundWindow()
	var pid uint32
	if owner != 0 {
		windows.GetWindowThreadProcessId(owner, &pid)
		if pid != uint32(os.Getpid()) {
			owner = 0
		}
	}
	if r := pfCall(dlg, vtShow, uintptr(owner)); r != 0 {
		if uint32(r) == hrCancelled {
			return "", ErrPickCancelled
		}
		return "", errors.New("the Windows folder picker failed")
	}
	var item uintptr
	if r := pfCall(dlg, vtGetResult, uintptr(unsafe.Pointer(&item))); r != 0 || item == 0 {
		return "", errors.New("no folder was picked")
	}
	defer pfCall(item, vtRelease)
	var name *uint16
	if r := pfCall(item, vtItemGetDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&name))); r != 0 || name == nil {
		return "", errors.New("that place is not a folder on a drive")
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(name))
	return windows.UTF16PtrToString(name), nil
}
