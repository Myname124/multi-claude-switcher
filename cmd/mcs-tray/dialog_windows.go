//go:build windows

package main

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf16"
)

// psEnc encodes a PowerShell script as base64/UTF-16LE for -EncodedCommand,
// sidestepping all command-line quoting issues.
func psEnc(script string) string {
	u16 := utf16.Encode([]rune(script))
	buf := make([]byte, 0, len(u16)*2)
	for _, c := range u16 {
		buf = append(buf, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// runPS runs a PowerShell script (STA, for WinForms) and returns trimmed stdout
// plus the run error (a non-zero exit surfaces as *exec.ExitError). hideConsole
// suppresses the console window (CREATE_NO_WINDOW) without hiding the GUI dialog.
func runPS(script string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-STA", "-EncodedCommand", psEnc(script))
	hideConsole(cmd)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// psQuote wraps a string as a single-quoted PowerShell literal.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// psTopmostOwner builds $owner, an invisible always-on-top form. Passing it as a
// MessageBox owner makes the box appear in FRONT of other apps — essential
// because the tray is a background process, so its dialogs would otherwise open
// behind the focused window (e.g. Claude) where the user never sees them.
const psTopmostOwner = `Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true; $owner.ShowInTaskbar = $false; $owner.FormBorderStyle = 'FixedToolWindow'
$owner.Opacity = 0; $owner.StartPosition = 'Manual'
$owner.Location = New-Object System.Drawing.Point(-3000, -3000)
$owner.Size = New-Object System.Drawing.Size(1, 1)
[void]$owner.Show()
`

// toastAppID is the identity toasts are shown under. It is registered per user
// under HKCU\Software\Classes\AppUserModelId, which is all Windows needs to
// show a toast from an unpackaged app, titled with the DisplayName.
//
// Toasts used to borrow PowerShell's app id instead. That id has its own
// on/off switch in Settings > Notifications, and on a real machine it was off
// (the notifier reported DisabledForApplication): every toast, including the
// answer to "Check for updates", was dropped without a trace.
const toastAppID = "miou1107.MultiClaudeSwitcher"

// notify shows a best-effort Windows toast notification. Fired detached so it
// never blocks the caller. A toast (rather than a NotifyIcon balloon) avoids
// adding a second tray icon. The script registers toastAppID first; that is
// idempotent and cheap, and doing it here rather than once at startup or in the
// installer means a toast can never go out under an id that is not registered.
// If the user turns notifications off for the app, it simply does nothing.
func notify(title, text string) {
	script := fmt.Sprintf(`$ErrorActionPreference = "SilentlyContinue"
$appId = %s
$key = "HKCU:\Software\Classes\AppUserModelId\$appId"
New-Item -Path $key -Force > $null
Set-ItemProperty -Path $key -Name DisplayName -Value "Multi-Claude Switcher"
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime] > $null
$t = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$n = $t.GetElementsByTagName("text")
$n.Item(0).AppendChild($t.CreateTextNode(%s)) > $null
$n.Item(1).AppendChild($t.CreateTextNode(%s)) > $null
$toast = [Windows.UI.Notifications.ToastNotification]::new($t)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($appId).Show($toast)`, psQuote(toastAppID), psQuote(title), psQuote(text))
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", psEnc(script))
	hideConsole(cmd)
	_ = cmd.Start()
}

// confirmDialog shows a Yes/No message box (in front, via a topmost owner); Yes =
// confirm. The action verb is carried in the message text (a standard box cannot
// relabel its buttons).
func confirmDialog(message, confirmLabel string) bool {
	_ = confirmLabel
	script := psTopmostOwner + `
$r = [System.Windows.Forms.MessageBox]::Show($owner, ` + psQuote(message) + `, 'Multi-Claude Switcher', 'YesNo', 'Question')
$owner.Close()
if ($r -eq 'Yes') { exit 0 } else { exit 1 }`
	_, err := runPS(script)
	return err == nil
}

// infoDialog shows an OK-only information box, in front via a topmost owner.
func infoDialog(title, message string) {
	script := psTopmostOwner + `
[void][System.Windows.Forms.MessageBox]::Show($owner, ` + psQuote(message) + `, ` + psQuote(title) + `, 'OK', 'Information')
$owner.Close()`
	_, _ = runPS(script)
}

// askText shows an always-on-top text-input dialog and returns the entered
// string, or "" if cancelled. A custom TopMost form is used instead of
// VisualBasic's InputBox so the prompt reliably appears in front of other windows.
func askText(prompt, defaultAnswer string) string {
	script := `Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$form = New-Object System.Windows.Forms.Form
$form.Text = 'Multi-Claude Switcher'; $form.TopMost = $true
$form.Width = 470; $form.Height = 210; $form.StartPosition = 'CenterScreen'
$form.FormBorderStyle = 'FixedDialog'; $form.MinimizeBox = $false; $form.MaximizeBox = $false
$label = New-Object System.Windows.Forms.Label
$label.Text = ` + psQuote(prompt) + `
$label.Left = 14; $label.Top = 14; $label.Width = 434; $label.Height = 78
$tb = New-Object System.Windows.Forms.TextBox
$tb.Left = 14; $tb.Top = 98; $tb.Width = 434; $tb.Text = ` + psQuote(defaultAnswer) + `
$ok = New-Object System.Windows.Forms.Button
$ok.Text = '确定'; $ok.Left = 272; $ok.Top = 130; $ok.Width = 80; $ok.DialogResult = 'OK'
$cancel = New-Object System.Windows.Forms.Button
$cancel.Text = '取消'; $cancel.Left = 360; $cancel.Top = 130; $cancel.Width = 88; $cancel.DialogResult = 'Cancel'
$form.Controls.AddRange(@($label, $tb, $ok, $cancel))
$form.AcceptButton = $ok; $form.CancelButton = $cancel
$form.Add_Shown({ $form.Activate(); $tb.Focus() })
if ($form.ShowDialog() -eq 'OK') { $tb.Text } else { '' }`
	out, err := runPS(script)
	if err != nil {
		return ""
	}
	return out
}
