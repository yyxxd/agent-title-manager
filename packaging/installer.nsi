Unicode true
Name "Agent Title Manager"
OutFile "${OUTPUT}"
InstallDir "$LOCALAPPDATA\Programs\AgentTitleManager"
RequestExecutionLevel user
SetCompressor /SOLID lzma
BrandingText "Agent Title Manager ${VERSION}"
VIProductVersion "${VERSION}.0"
VIAddVersionKey /LANG=2052 "ProductName" "Agent Title Manager"
VIAddVersionKey /LANG=2052 "FileDescription" "Agent Title Manager 安装程序"
VIAddVersionKey /LANG=2052 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=2052 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=2052 "LegalCopyright" "Agent Title Manager contributors"

; MUI2 applies its own Icon/UninstallIcon from these defines.
!define MUI_ICON "app.ico"
!define MUI_UNICON "app.ico"
!include "MUI2.nsh"
!include "FileFunc.nsh"
!include "x64.nsh"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"

Var Isolated
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\AgentTitleManager"
!define STARTUP_KEY "Software\Microsoft\Windows\CurrentVersion\Run"
!define WEBVIEW_KEY "SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP|MB_OK "此版本需要 64 位 Windows。" /SD IDOK
    SetErrorLevel 2
    Abort
  ${EndIf}
  ${GetParameters} $0
  ${GetOptions} $0 "/NoIntegration" $1
  IfErrors +3
    StrCpy $Isolated 1
    Return
  ReadRegStr $0 HKCU "${UNINSTALL_KEY}" "InstallLocation"
  StrCmp $0 "" +2
    StrCpy $INSTDIR $0
FunctionEnd

Section "Agent Title Manager"
  SetRegView 32
  ReadRegStr $0 HKLM "${WEBVIEW_KEY}" "pv"
  StrCmp $0 "" 0 runtimeFound
  ReadRegStr $0 HKCU "${WEBVIEW_KEY}" "pv"
  StrCmp $0 "" 0 runtimeFound
  MessageBox MB_ICONSTOP|MB_OK "请安装 Microsoft Edge WebView2 Runtime，再重新运行此安装程序。" /SD IDOK
  SetErrorLevel 2
  Abort
runtimeFound:
  Call RequireClosedForUpgrade
  SetShellVarContext current
  SetOutPath "$INSTDIR"
  File "${PAYLOAD}\AgentTitleManager.exe"
  File "${PAYLOAD}\atm-bridge.exe"
  File "${PAYLOAD}\atm-update.exe"
  File "${PAYLOAD}\LICENSE.txt"
  File "${PAYLOAD}\OIL-CODEX-TITLE-LICENSE.txt"
  SetOutPath "$INSTDIR\plugins"
  File /r "${PAYLOAD}\plugins\*.*"
  SetOutPath "$INSTDIR"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  StrCmp $Isolated 1 done
  CreateShortCut "$SMPROGRAMS\Agent Title Manager.lnk" "$INSTDIR\AgentTitleManager.exe" "" "$INSTDIR\AgentTitleManager.exe" 0
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "Agent Title Manager"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\AgentTitleManager.exe"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1
  ; SHCNE_UPDATEITEM / SHCNF_PATHW: refresh only the replaced EXE and Start menu shortcut.
  System::Call 'shell32::SHChangeNotify(i 0x2000, i 0x5, w "$INSTDIR\AgentTitleManager.exe", p 0)'
  System::Call 'shell32::SHChangeNotify(i 0x2000, i 0x5, w "$SMPROGRAMS\Agent Title Manager.lnk", p 0)'
done:
SectionEnd

Function RequireClosedForUpgrade
  IfFileExists "$INSTDIR\AgentTitleManager.exe" 0 ready
  ExecWait '"$INSTDIR\AgentTitleManager.exe" --check-uninstall' $0
  StrCmp $0 2 running ready
running:
  MessageBox MB_ICONEXCLAMATION|MB_OK "请先从托盘退出 Agent Title Manager，再安装新版本。" /SD IDOK
  SetErrorLevel 4
  Quit
ready:
FunctionEnd

Section "Uninstall"
  Call un.RequireReady
  SetShellVarContext current
  ReadRegStr $0 HKCU "${UNINSTALL_KEY}" "InstallLocation"
  StrCmp $0 $INSTDIR 0 removeFiles
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
  DeleteRegValue HKCU "${STARTUP_KEY}" "AgentTitleManager"
  Delete "$SMPROGRAMS\Agent Title Manager.lnk"
removeFiles:
  Delete "$INSTDIR\AgentTitleManager.exe"
  Delete "$INSTDIR\atm-bridge.exe"
  Delete "$INSTDIR\atm-update.exe"
  Delete "$INSTDIR\LICENSE.txt"
  Delete "$INSTDIR\OIL-CODEX-TITLE-LICENSE.txt"
  Delete "$INSTDIR\plugins\node.exe"
  Delete "$INSTDIR\plugins\NODE-LICENSE.txt"
  Delete "$INSTDIR\plugins\THIRD_PARTY_NOTICES.md"
  RMDir /r "$INSTDIR\plugins\codex"
  RMDir /r "$INSTDIR\plugins\claude"
  RMDir /r "$INSTDIR\plugins\pi"
  RMDir /r "$INSTDIR\plugins\example"
  RMDir "$INSTDIR\plugins"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
SectionEnd

Function un.RequireReady
  ClearErrors
  ExecWait '"$INSTDIR\AgentTitleManager.exe" --check-uninstall' $0
  IfErrors cannotCheck
  StrCmp $0 0 ready
  StrCmp $0 2 running
  MessageBox MB_ICONEXCLAMATION|MB_OK "请在“Agent 插件”中停用适配器并恢复接管的旧版插件。退出桌面应用后重新卸载。" /SD IDOK
  Goto blocked
running:
  MessageBox MB_ICONEXCLAMATION|MB_OK "Agent Title Manager 正在运行。请在托盘菜单中退出，再重新卸载。" /SD IDOK
  Goto blocked
cannotCheck:
  MessageBox MB_ICONEXCLAMATION|MB_OK "无法检查插件是否已安全停用。请修复安装后重新卸载。" /SD IDOK
blocked:
  SetErrorLevel 3
  Abort
ready:
FunctionEnd
