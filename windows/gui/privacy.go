//go:build windows

package main

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/PeaChack/Kordo/windows/privacy"
)

type capabilityAudit struct {
	ID     string              `json:"id"`
	Title  string              `json:"title"`
	About  string              `json:"about"`
	Device string              `json:"device"` // HKLM switch: Allow/Deny/""
	User   string              `json:"user"`   // HKCU switch
	Apps   []privacy.AppAccess `json:"apps"`
}

type privacyAudit struct {
	Region       string            `json:"region"`   // "Country or region" in Windows settings, ISO code
	Language     string            `json:"language"` // UI language, e.g. ru-RU
	Capabilities []capabilityAudit `json:"capabilities"`
	Browsers     []string          `json:"browsers"`
}

// PrivacyAudit reads the current user's privacy settings and which apps
// asked for location, Wi-Fi and Bluetooth (and when). It runs in the GUI
// because these live in the user's registry hive, not the service's.
func (a *App) PrivacyAudit() privacyAudit {
	out := privacyAudit{Region: privacy.Region(), Browsers: privacy.Browsers()}
	if langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME); err == nil && len(langs) > 0 {
		out.Language = langs[0]
	}
	for _, c := range privacy.Capabilities {
		apps := privacy.UserAccess(c.ID)
		if apps == nil {
			apps = []privacy.AppAccess{}
		}
		out.Capabilities = append(out.Capabilities, capabilityAudit{
			ID: c.ID, Title: c.Title, About: c.About,
			Device: privacy.Switch(registry.LOCAL_MACHINE, c.ID),
			User:   privacy.Switch(registry.CURRENT_USER, c.ID),
			Apps:   apps,
		})
	}
	if out.Browsers == nil {
		out.Browsers = []string{}
	}
	return out
}

// OpenSettings opens a page of Windows Settings (ms-settings: links only).
func (a *App) OpenSettings(uri string) error {
	if !strings.HasPrefix(uri, "ms-settings:") || strings.ContainsAny(uri, " \"&|") {
		return errors.New("not a settings link")
	}
	return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(uri), nil, nil, windows.SW_SHOWNORMAL)
}
