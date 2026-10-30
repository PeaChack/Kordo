; Kordo installer (NSIS 3). Built by build/build-installer.sh:
;   makensis -INPUTCHARSET UTF8 -DVERSION=0.3.0 -DWINVERSION=0.3.0.0 \
;            -DSRCDIR=<abs path to bin> -DOUTFILE=<abs path to setup exe> \
;            [-DICON=<.ico>] [-DSIGN_CMD=<signing command>] kordo.nsi
;
; Installs into %ProgramFiles%\Kordo and registers the KordoSvc service.
; Upgrades stop the service, replace the files and start it again; a service
; registered from another folder (a manual install) is removed and registered
; again from Program Files.

Unicode true
ManifestDPIAware true
ManifestSupportedOS Win10
RequestExecutionLevel admin
SetCompressor /SOLID lzma

!ifndef VERSION | WINVERSION | SRCDIR | OUTFILE
  !error "pass -DVERSION, -DWINVERSION, -DSRCDIR and -DOUTFILE (see build/build-installer.sh)"
!endif

!define PRODUCT     "Kordo"
!define PUBLISHER   "Kordo contributors"
!define URL         "https://github.com/PeaChack/Kordo"
!define SVC         "KordoSvc"
!define UNINST_KEY  "Software\Microsoft\Windows\CurrentVersion\Uninstall\Kordo"
!define RUN_KEY     "Software\Microsoft\Windows\CurrentVersion\Run"
!define SVC_KEY     "SYSTEM\CurrentControlSet\Services\${SVC}"
; WebView2 Runtime client id and the Evergreen bootstrapper download link
!define WV2_CLIENT  "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
!define WV2_URL     "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

; Code signing hook: the installer and the uninstaller are signed right after
; makensis writes them. Off unless SIGN_CMD is defined.
!ifdef SIGN_CMD
  !finalize '${SIGN_CMD} "%1"' = 0
  !uninstfinalize '${SIGN_CMD} "%1"' = 0
!endif

Name "${PRODUCT}"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\${PRODUCT}"
BrandingText "${PRODUCT} ${VERSION}"
ShowInstDetails show
ShowUninstDetails show

VIProductVersion "${WINVERSION}"
VIFileVersion "${WINVERSION}"
VIAddVersionKey /LANG=0 "ProductName" "${PRODUCT}"
VIAddVersionKey /LANG=0 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=0 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=0 "FileDescription" "${PRODUCT} Setup"
VIAddVersionKey /LANG=0 "CompanyName" "${PUBLISHER}"
VIAddVersionKey /LANG=0 "LegalCopyright" "GPL-3.0-or-later"

!include MUI2.nsh
!include LogicLib.nsh
!include x64.nsh
!include WinVer.nsh
!include FileFunc.nsh

!ifdef ICON
  !define MUI_ICON "${ICON}"
  !define MUI_UNICON "${ICON}"
!endif
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_TEXT "$(RunKordo)"
!define MUI_FINISHPAGE_RUN_FUNCTION LaunchAsUser

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "Russian"
!insertmacro MUI_LANGUAGE "English"

LangString RunKordo        ${LANG_RUSSIAN} "Запустить Kordo"
LangString RunKordo        ${LANG_ENGLISH} "Run Kordo"
LangString Need64          ${LANG_RUSSIAN} "Kordo работает только в 64-битной Windows 10 или 11."
LangString Need64          ${LANG_ENGLISH} "Kordo requires 64-bit Windows 10 or 11."
LangString ClosingGui      ${LANG_RUSSIAN} "Закрываю Kordo…"
LangString ClosingGui      ${LANG_ENGLISH} "Closing Kordo…"
LangString StoppingSvc     ${LANG_RUSSIAN} "Останавливаю службу ${SVC}…"
LangString StoppingSvc     ${LANG_ENGLISH} "Stopping the ${SVC} service…"
LangString MovingSvc       ${LANG_RUSSIAN} "Служба ${SVC} установлена из другой папки, удаляю её:"
LangString MovingSvc       ${LANG_ENGLISH} "The ${SVC} service is registered from another folder, removing it:"
LangString InstallingSvc   ${LANG_RUSSIAN} "Регистрирую и запускаю службу ${SVC}…"
LangString InstallingSvc   ${LANG_ENGLISH} "Registering and starting the ${SVC} service…"
LangString StartingSvc     ${LANG_RUSSIAN} "Запускаю службу ${SVC}…"
LangString StartingSvc     ${LANG_ENGLISH} "Starting the ${SVC} service…"
LangString SvcFailed       ${LANG_RUSSIAN} "Не удалось запустить службу ${SVC} (код $0). Подробности в журнале установки. Можно повторить вручную от администратора: kordo-svc.exe install"
LangString SvcFailed       ${LANG_ENGLISH} "Could not start the ${SVC} service (code $0). See the install log. You can retry as administrator: kordo-svc.exe install"
LangString SvcStuck        ${LANG_RUSSIAN} "Старая служба ${SVC} ещё не удалена (возможно, открыта оснастка «Службы»). Закройте её и нажмите «Повтор»."
LangString SvcStuck        ${LANG_ENGLISH} "The old ${SVC} service is not removed yet (the Services console may be open). Close it and press Retry."
LangString NoWebView2      ${LANG_RUSSIAN} "Не найден Microsoft Edge WebView2 Runtime — без него окно Kordo не откроется (служба работать будет).$\n$\nСкачать и установить его сейчас (около 2 МБ, загрузчик Microsoft)?"
LangString NoWebView2      ${LANG_ENGLISH} "Microsoft Edge WebView2 Runtime was not found. Kordo's window needs it (the service works without it).$\n$\nDownload and install it now (about 2 MB, Microsoft bootstrapper)?"
LangString WebView2Failed  ${LANG_RUSSIAN} "Не удалось установить WebView2 Runtime. Сейчас откроется страница загрузки: ${WV2_URL}"
LangString WebView2Failed  ${LANG_ENGLISH} "Could not install WebView2 Runtime. The download page will open now: ${WV2_URL}"
LangString WebView2Skipped ${LANG_RUSSIAN} "ВНИМАНИЕ: WebView2 Runtime не установлен. Скачайте его: ${WV2_URL}"
LangString WebView2Skipped ${LANG_ENGLISH} "WARNING: WebView2 Runtime is not installed. Get it at ${WV2_URL}"
LangString RemoveData      ${LANG_RUSSIAN} "Удалить настройки и серверы Kordo ($APPDATA\Kordo)?$\n$\nЕсли собираетесь поставить Kordo снова, выберите «Нет»."
LangString RemoveData      ${LANG_ENGLISH} "Remove Kordo settings and servers ($APPDATA\Kordo)?$\n$\nChoose No if you plan to install Kordo again."
LangString UnSvcFailed     ${LANG_RUSSIAN} "kordo-svc uninstall завершился с кодом $0. Если остались фильтры брандмауэра, снимите их: kordoctl killswitch-clear или повторите удаление."
LangString UnSvcFailed     ${LANG_ENGLISH} "kordo-svc uninstall exited with code $0. If firewall filters remain, run kordoctl killswitch-clear or uninstall again."

Var SvcMode ; "" = no service yet, "same" = upgrade in place, "moved" = removed a service from another folder

; ---------------------------------------------------------------- helpers

; Closes every running Kordo.exe (GUI): politely first, then forcefully.
!macro CLOSE_GUI un
Function ${un}CloseGui
  DetailPrint "$(ClosingGui)"
  nsExec::ExecToLog '"$SYSDIR\taskkill.exe" /IM Kordo.exe'
  Pop $0
  ${If} $0 == 0
    Sleep 2000
  ${EndIf}
  nsExec::ExecToLog '"$SYSDIR\taskkill.exe" /F /IM Kordo.exe'
  Pop $0
FunctionEnd
!macroend
!insertmacro CLOSE_GUI ""
!insertmacro CLOSE_GUI "un."

; $R0 = image path of the KordoSvc service without quotes and arguments, "" if none.
Function ReadServicePath
  ReadRegStr $R0 HKLM "${SVC_KEY}" "ImagePath"
  ${If} $R0 == ""
    Return
  ${EndIf}
  StrCpy $R1 $R0 1
  ${If} $R1 == '"'
    StrCpy $R0 $R0 "" 1
    StrLen $R2 $R0
    StrCpy $R3 0
    ${Do}
      ${If} $R3 >= $R2
        ${Break}
      ${EndIf}
      StrCpy $R1 $R0 1 $R3
      ${If} $R1 == '"'
        StrCpy $R0 $R0 $R3
        ${Break}
      ${EndIf}
      IntOp $R3 $R3 + 1
    ${Loop}
  ${EndIf}
FunctionEnd

; Runs a non-elevated process through Explorer (the installer itself is elevated).
Function LaunchAsUser
  Exec '"$WINDIR\explorer.exe" "$INSTDIR\Kordo.exe"'
FunctionEnd

Function .onInit
  ${IfNot} ${RunningX64}
  ${OrIfNot} ${AtLeastWin10}
    MessageBox MB_ICONSTOP "$(Need64)" /SD IDOK
    Abort
  ${EndIf}
  SetRegView 64
  SetShellVarContext all
  ; an upgrade goes to the folder of the previous installation
  ReadRegStr $0 HKLM "${UNINST_KEY}" "InstallLocation"
  ${If} $0 != ""
    StrCpy $INSTDIR $0
  ${EndIf}
FunctionEnd

; ---------------------------------------------------------------- install

Section "Kordo" SecMain
  SectionIn RO
  InitPluginsDir
  ; the new kordo-svc.exe from a temporary folder handles the old service:
  ; its "stop" waits until the service has really stopped
  File "/oname=$PLUGINSDIR\kordo-svc.exe" "${SRCDIR}\kordo-svc.exe"

  Call CloseGui

  StrCpy $SvcMode ""
  Call ReadServicePath
  ${If} $R0 != ""
    ${If} $R0 == "$INSTDIR\kordo-svc.exe" ; NSIS compares case-insensitively
      StrCpy $SvcMode "same"
      DetailPrint "$(StoppingSvc)"
      nsExec::ExecToLog '"$PLUGINSDIR\kordo-svc.exe" stop'
      Pop $0
      ${If} $0 != 0
        nsExec::ExecToLog '"$SYSDIR\taskkill.exe" /F /IM kordo-svc.exe'
        Pop $0
      ${EndIf}
      Sleep 1000 ; let the process exit and release its files
    ${Else}
      StrCpy $SvcMode "moved"
      DetailPrint "$(MovingSvc) $R0"
      ; stops it, deletes it and removes its firewall filters
      nsExec::ExecToLog '"$PLUGINSDIR\kordo-svc.exe" uninstall'
      Pop $0
      ${If} $0 != 0
        nsExec::ExecToLog '"$SYSDIR\sc.exe" stop ${SVC}'
        Pop $0
        nsExec::ExecToLog '"$SYSDIR\sc.exe" delete ${SVC}'
        Pop $0
      ${EndIf}
      ; the SCM drops the registration once every handle to it is closed
      ${Do}
        StrCpy $1 0
        ${Do}
          Call ReadServicePath
          ${If} $R0 == ""
          ${OrIf} $1 > 40
            ${Break}
          ${EndIf}
          IntOp $1 $1 + 1
          Sleep 250
        ${Loop}
        ${If} $R0 == ""
          ${Break}
        ${EndIf}
        MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "$(SvcStuck)" /SD IDCANCEL IDRETRY svc_retry
        ${Break}
      svc_retry:
      ${Loop}
    ${EndIf}
  ${EndIf}

  SetOutPath "$INSTDIR"
  SetOverwrite on
  File "${SRCDIR}\Kordo.exe"
  File "${SRCDIR}\kordo-svc.exe"
  File "${SRCDIR}\kordoctl.exe"
  File "${SRCDIR}\LICENSE"
  File "${SRCDIR}\THIRD_PARTY_NOTICES.md"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  ${If} $SvcMode == "same"
    DetailPrint "$(StartingSvc)"
    nsExec::ExecToLog '"$INSTDIR\kordo-svc.exe" start'
  ${Else}
    DetailPrint "$(InstallingSvc)"
    nsExec::ExecToLog '"$INSTDIR\kordo-svc.exe" install'
  ${EndIf}
  Pop $0
  ${If} $0 != 0
    DetailPrint "$(SvcFailed)"
    MessageBox MB_ICONEXCLAMATION "$(SvcFailed)" /SD IDOK
  ${EndIf}

  CreateShortcut "$SMPROGRAMS\Kordo.lnk" "$INSTDIR\Kordo.exe" "" "$INSTDIR\Kordo.exe" 0

  ; An existing "open at login" entry may point to the old folder. Kordo fixes
  ; it on its next start too; this covers the user who runs the installer.
  ReadRegStr $0 HKCU "${RUN_KEY}" "Kordo"
  ${If} $0 != ""
    WriteRegStr HKCU "${RUN_KEY}" "Kordo" '"$INSTDIR\Kordo.exe" --minimized'
  ${EndIf}
  DeleteRegValue HKCU "${RUN_KEY}" "Portico"

  ; Programs and Features
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayName" "${PRODUCT}"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayIcon" '"$INSTDIR\Kordo.exe",0'
  WriteRegStr HKLM "${UNINST_KEY}" "Publisher" "${PUBLISHER}"
  WriteRegStr HKLM "${UNINST_KEY}" "URLInfoAbout" "${URL}"
  WriteRegStr HKLM "${UNINST_KEY}" "HelpLink" "${URL}"
  WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKLM "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKLM "${UNINST_KEY}" "EstimatedSize" $0
SectionEnd

; WebView2 Runtime: present on Windows 11, may be missing on Windows 10.
Section "-WebView2"
  SetRegView 32 ; EdgeUpdate lives in the 32-bit view
  ReadRegStr $0 HKLM "${WV2_CLIENT}" "pv"
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    ReadRegStr $0 HKCU "${WV2_CLIENT}" "pv"
  ${EndIf}
  SetRegView 64
  ${If} $0 != ""
  ${AndIf} $0 != "0.0.0.0"
    Return
  ${EndIf}
  MessageBox MB_YESNO|MB_ICONEXCLAMATION "$(NoWebView2)" /SD IDNO IDYES download
  DetailPrint "$(WebView2Skipped)"
  Return
download:
  nsExec::ExecToLog `powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "$$ProgressPreference='SilentlyContinue'; Invoke-WebRequest -UseBasicParsing -Uri '${WV2_URL}' -OutFile '$PLUGINSDIR\MicrosoftEdgeWebview2Setup.exe'"`
  Pop $0
  ${If} $0 == 0
    ExecWait '"$PLUGINSDIR\MicrosoftEdgeWebview2Setup.exe" /silent /install' $0
  ${EndIf}
  ${If} $0 != 0
    DetailPrint "$(WebView2Failed)"
    MessageBox MB_ICONEXCLAMATION "$(WebView2Failed)" /SD IDOK
    Exec '"$WINDIR\explorer.exe" "${WV2_URL}"'
  ${EndIf}
SectionEnd

; ---------------------------------------------------------------- uninstall

Function un.onInit
  SetRegView 64
  SetShellVarContext all
FunctionEnd

Section "Uninstall"
  Call un.CloseGui

  ; Before the files go: stops and deletes the service and removes every WFP
  ; filter, otherwise the kill switch could outlive Kordo.
  ${If} ${FileExists} "$INSTDIR\kordo-svc.exe"
    nsExec::ExecToLog '"$INSTDIR\kordo-svc.exe" uninstall'
    Pop $0
    ${If} $0 != 0
      DetailPrint "$(UnSvcFailed)"
      MessageBox MB_ICONEXCLAMATION "$(UnSvcFailed)" /SD IDOK
    ${EndIf}
  ${EndIf}

  Delete "$INSTDIR\Kordo.exe"
  Delete "$INSTDIR\kordo-svc.exe"
  Delete "$INSTDIR\kordoctl.exe"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\THIRD_PARTY_NOTICES.md"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"

  Delete "$SMPROGRAMS\Kordo.lnk"
  DeleteRegValue HKCU "${RUN_KEY}" "Kordo"
  DeleteRegKey HKCU "Software\Kordo"
  DeleteRegKey HKLM "${UNINST_KEY}"

  ; settings, servers and logs (%ProgramData%\Kordo): kept unless asked
  MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2 "$(RemoveData)" /SD IDNO IDNO keep
  RMDir /r "$APPDATA\Kordo"
keep:
SectionEnd
