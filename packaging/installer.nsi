Unicode true
Name "Agent Title Manager"
OutFile "${OUTPUT}"
InstallDir "$LOCALAPPDATA\Programs\AgentTitleManager"
RequestExecutionLevel user
SetCompressor /SOLID lzma
BrandingText "Agent Title Manager"
VIProductVersion "${VERSION}.0"
VIAddVersionKey /LANG=2052 "ProductName" "Agent Title Manager"
VIAddVersionKey /LANG=2052 "FileDescription" "Agent Title Manager 安装程序"
VIAddVersionKey /LANG=2052 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=2052 "LegalCopyright" "Agent Title Manager contributors"

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
  SetShellVarContext current
  SetOutPath "$INSTDIR"
  File "${PAYLOAD}\AgentTitleManager.exe"
  File "${PAYLOAD}\atm-bridge.exe"
  File "${PAYLOAD}\LICENSE.txt"
  File "${PAYLOAD}\OIL-CODEX-TITLE-LICENSE.txt"
  SetOutPath "$INSTDIR\plugins"
  File /r "${PAYLOAD}\plugins\*.*"
  SetOutPath "$INSTDIR"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  StrCmp $Isolated 1 done
  CreateShortCut "$SMPROGRAMS\Agent Title Manager.lnk" "$INSTDIR\AgentTitleManager.exe"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "Agent Title Manager"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\AgentTitleManager.exe"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1
done:
SectionEnd

Section "Uninstall"
  SetShellVarContext current
  ReadRegStr $0 HKCU "${UNINSTALL_KEY}" "InstallLocation"
  StrCmp $0 $INSTDIR 0 removeFiles
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
  DeleteRegValue HKCU "${STARTUP_KEY}" "AgentTitleManager"
  Delete "$SMPROGRAMS\Agent Title Manager.lnk"
removeFiles:
  Delete "$INSTDIR\AgentTitleManager.exe"
  Delete "$INSTDIR\atm-bridge.exe"
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
