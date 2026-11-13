; GoDownloader Windows Desktop Beta - NSIS Installer Fixture
; DSK-4E Implementation Milestone

!include "MUI2.nsh"
!include "x64.nsh"
!include "LogicLib.nsh"

!ifndef BIN_DIR
    !define BIN_DIR "..\..\bin"
!endif

Name "GoDownloader"
OutFile "${BIN_DIR}\GoDownloader-0.9.1-beta-windows-amd64-setup.exe"
InstallDir "$LOCALAPPDATA\Programs\GoDownloader"
InstallDirRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GoDownloader" "InstallLocation"
RequestExecutionLevel user

VIProductVersion "0.9.1.0"
VIAddVersionKey "ProductName" "GoDownloader"
VIAddVersionKey "ProductVersion" "0.9.1"
VIAddVersionKey "FileVersion" "0.9.1.0"
VIAddVersionKey "FileDescription" "GoDownloader Windows Desktop Beta Setup"
VIAddVersionKey "LegalCopyright" "© 2026 GoDownloader"

!define MUI_ICON "icon.ico"
!define MUI_UNICON "icon.ico"
!define MUI_ABORTWARNING

!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Function .onInit
    SetShellVarContext current

    ; 1. Require 64-bit Windows
    ${IfNot} ${RunningX64}
        MessageBox MB_ICONSTOP|MB_OK "GoDownloader Windows Desktop Beta requires 64-bit Windows." /SD IDOK
        Abort
    ${EndIf}

    ; 2. Check for WebView2 Evergreen runtime
    Call CheckWebView2
    Pop $0
    ${If} $0 != "1"
        MessageBox MB_ICONSTOP|MB_OK "GoDownloader needs the Microsoft Edge WebView2 Runtime, but it was not found on this computer.$\r$\n$\r$\nInstall the Evergreen WebView2 Runtime from Microsoft (https://developer.microsoft.com/microsoft-edge/webview2/), then run setup again." /SD IDOK
        Abort
    ${EndIf}

    ; 3. Check for running GoDownloader core via exclusive lock
    Call CheckRunningCore
    Pop $0
    ${If} $0 == "1"
        MessageBox MB_ICONSTOP|MB_OK "Another GoDownloader runtime is currently active.$\r$\nPlease close GoDownloader and try again." /SD IDOK
        Abort
    ${EndIf}
FunctionEnd

Function CheckWebView2
    ; Microsoft-documented detection ("Detect if a WebView2 Runtime is already installed"): the Evergreen
    ; WebView2 Runtime is installed when the `pv` (REG_SZ) value of its EdgeUpdate client key
    ; {F3017226-FE2A-4295-8BDF-00C3A9A7E4C5} holds a version other than empty or 0.0.0.0, either per-machine
    ; (HKLM through the 32-bit registry view: WOW6432Node on 64-bit Windows, the native key on 32-bit Windows)
    ; or per-user (HKCU). No minimum version is checked.
    SetRegView 32
    ReadRegStr $1 HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
    SetRegView default
    ${If} $1 != ""
    ${AndIf} $1 != "0.0.0.0"
        Push "1"
        Return
    ${EndIf}

    ReadRegStr $1 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
    ${If} $1 != ""
    ${AndIf} $1 != "0.0.0.0"
        Push "1"
        Return
    ${EndIf}

    Push "0"
FunctionEnd

Function CheckRunningCore
    ; If lock file exists, attempt exclusive write open
    IfFileExists "$APPDATA\GoDownloader\downloader.db.runtime.lock" 0 lock_not_found
    ClearErrors
    FileOpen $0 "$APPDATA\GoDownloader\downloader.db.runtime.lock" "w"
    IfErrors lock_held
    FileClose $0
    Push "0"
    Return
lock_held:
    Push "1"
    Return
lock_not_found:
    Push "0"
FunctionEnd

Section "Install" SecInstall
    SetShellVarContext current
    SetOutPath "$INSTDIR"

    ; Deliver payload binary
    File "/oname=GoDownloader.exe" "${BIN_DIR}\GoDownloader.exe"

    ; Generate uninstaller
    WriteUninstaller "$INSTDIR\Uninstall.exe"

    ; Create Start Menu shortcut
    CreateDirectory "$SMPROGRAMS\GoDownloader"
    CreateShortcut "$SMPROGRAMS\GoDownloader\GoDownloader.lnk" "$INSTDIR\GoDownloader.exe" "" "$INSTDIR\GoDownloader.exe" 0

    ; Register uninstall entry in HKCU
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GoDownloader" "DisplayName" "GoDownloader"
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GoDownloader" "DisplayVersion" "0.9.1"
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GoDownloader" "Publisher" "GoDownloader"
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GoDownloader" "InstallLocation" "$INSTDIR"
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GoDownloader" "UninstallString" '"$INSTDIR\Uninstall.exe"'
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GoDownloader" "DisplayIcon" "$INSTDIR\GoDownloader.exe,0"
    WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GoDownloader" "NoModify" 1
    WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GoDownloader" "NoRepair" 1
SectionEnd

Function un.onInit
    SetShellVarContext current

    ; Check if running core is holding the runtime lock
    IfFileExists "$APPDATA\GoDownloader\downloader.db.runtime.lock" 0 un_lock_not_found
    ClearErrors
    FileOpen $0 "$APPDATA\GoDownloader\downloader.db.runtime.lock" "w"
    IfErrors un_lock_held
    FileClose $0
    Return
un_lock_held:
    MessageBox MB_ICONSTOP|MB_OK "GoDownloader is currently running.$\r$\nPlease close GoDownloader and try again." /SD IDOK
    Abort
un_lock_not_found:
FunctionEnd

Section "Uninstall"
    SetShellVarContext current

    ; 1. Remove binaries
    Delete "$INSTDIR\GoDownloader.exe"
    Delete "$INSTDIR\Uninstall.exe"
    RMDir "$INSTDIR"

    ; 2. Remove shortcuts
    Delete "$SMPROGRAMS\GoDownloader\GoDownloader.lnk"
    RMDir "$SMPROGRAMS\GoDownloader"

    ; 3. Clean stale autostart value ONLY if it references the uninstalled binary
    ReadRegStr $0 HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "godownloader"
    ${If} $0 != ""
        ; Check if the Run entry corresponds to this installed executable path
        ${If} $0 == '"$INSTDIR\GoDownloader.exe" --background'
            DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "godownloader"
        ${ElseIf} $0 == '$INSTDIR\GoDownloader.exe --background'
            DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "godownloader"
        ${ElseIf} $0 == '"$INSTDIR\GoDownloader.exe"'
            DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "godownloader"
        ${ElseIf} $0 == '$INSTDIR\GoDownloader.exe'
            DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "godownloader"
        ${EndIf}
    ${EndIf}

    ; 4. Remove uninstall registry entry
    DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GoDownloader"

    ; NOTE: %APPDATA%\GoDownloader and user download destinations are strictly PRESERVED!
SectionEnd
