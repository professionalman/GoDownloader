//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

func showNativeCollisionError() {
	title, _ := windows.UTF16PtrFromString("GoDownloader")
	msg, _ := windows.UTF16PtrFromString("Another GoDownloader runtime is already using this data store.\n\nClose the other GoDownloader process and try again.")
	windows.MessageBox(0, msg, title, windows.MB_OK|windows.MB_ICONERROR)
}
