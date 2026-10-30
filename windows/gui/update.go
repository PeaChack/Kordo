//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/PeaChack/Kordo/core/version"
)

// appVersion is set at build time: -X main.appVersion=0.3.0 (build/build-installer.sh,
// build/build.ps1). "dev" builds never offer updates.
var appVersion = "dev"

const (
	releasesPage  = "https://github.com/PeaChack/Kordo/releases"
	latestRelease = "https://api.github.com/repos/PeaChack/Kordo/releases/latest"
	maxInstaller  = 200 << 20
)

// UpdateInfo is what the window shows about updates.
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	URL       string `json:"url,omitempty"` // release page
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	HTMLURL    string    `json:"html_url"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

// Version returns the version of this build.
func (a *App) Version() string { return appVersion }

// CheckUpdate asks GitHub for the latest release (drafts and prereleases are
// not returned by /releases/latest).
func (a *App) CheckUpdate() (*UpdateInfo, error) {
	info := &UpdateInfo{Current: appVersion}
	if !version.Valid(appVersion) {
		return info, nil
	}
	rel, err := fetchLatest(a.ctx)
	if err != nil {
		return info, err
	}
	info.Latest = strings.TrimPrefix(rel.TagName, "v")
	info.URL = rel.HTMLURL
	info.Available = version.Compare(info.Latest, appVersion) > 0 && installerAsset(rel) != nil
	return info, nil
}

// InstallUpdate downloads the installer of the latest release, checks it
// against the release's .sha256 file and starts it. Windows asks for
// administrator rights; the installer closes this window itself.
func (a *App) InstallUpdate() error {
	rel, err := fetchLatest(a.ctx)
	if err != nil {
		return err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if version.Compare(latest, appVersion) <= 0 {
		return errors.New("установлена последняя версия")
	}
	exe := installerAsset(rel)
	if exe == nil {
		return errors.New("в релизе нет установщика")
	}
	var sumAsset *ghAsset
	for i := range rel.Assets {
		if strings.EqualFold(rel.Assets[i].Name, exe.Name+".sha256") {
			sumAsset = &rel.Assets[i]
		}
	}
	if sumAsset == nil {
		return errors.New("в релизе нет контрольной суммы установщика")
	}
	sumText, err := download(a.ctx, sumAsset.URL, 4096, 30*time.Second, nil)
	if err != nil {
		return fmt.Errorf("загрузка контрольной суммы: %w", err)
	}
	fields := strings.Fields(string(sumText))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return errors.New("неверный формат контрольной суммы")
	}
	want := strings.ToLower(fields[0])

	dir := updateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, filepath.Base(exe.Name))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = download(a.ctx, exe.URL, maxInstaller, 15*time.Minute, io.MultiWriter(f, h))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return fmt.Errorf("загрузка установщика: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(path)
		return errors.New("контрольная сумма установщика не совпала, файл удалён")
	}
	// A signed build only installs signed updates: the SHA-256 file comes
	// from the same release, so it guards against a broken download, not
	// against a replaced release.
	if selfSigned() {
		if err := verifySignature(path); err != nil {
			os.Remove(path)
			return fmt.Errorf("у установщика нет действительной подписи, файл удалён: %w", err)
		}
	}
	// ShellExecute (unlike CreateProcess) shows the UAC prompt the installer's
	// manifest asks for.
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(path)
	if err := windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return errors.New("установка отменена")
		}
		return fmt.Errorf("запуск установщика: %w", err)
	}
	return nil
}

// OpenReleasePage opens a Kordo release page in the default browser.
func (a *App) OpenReleasePage(url string) {
	if !strings.HasPrefix(url, releasesPage) {
		url = releasesPage
	}
	runtime.BrowserOpenURL(a.ctx, url)
}

// GetUpdateCheck reports whether the window checks for updates at start.
func (a *App) GetUpdateCheck() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Kordo`, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("CheckUpdates")
	return err != nil || v != 0
}

func (a *App) SetUpdateCheck(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Kordo`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	var v uint32
	if on {
		v = 1
	}
	return k.SetDWordValue("CheckUpdates", v)
}

func installerAsset(rel *ghRelease) *ghAsset {
	for i := range rel.Assets {
		n := strings.ToLower(rel.Assets[i].Name)
		if strings.HasPrefix(n, "kordo-setup-") && strings.HasSuffix(n, ".exe") {
			return &rel.Assets[i]
		}
	}
	return nil
}

func fetchLatest(ctx context.Context) (*ghRelease, error) {
	body, err := download(ctx, latestRelease, 1<<20, 20*time.Second, nil)
	if err != nil {
		return nil, fmt.Errorf("проверка обновлений: %w", err)
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("проверка обновлений: %w", err)
	}
	if rel.TagName == "" || rel.Draft || rel.Prerelease {
		return nil, errors.New("проверка обновлений: релизов пока нет")
	}
	return &rel, nil
}

// download fetches url. With w == nil the body (at most limit bytes) is
// returned, otherwise it is streamed into w.
func download(ctx context.Context, url string, limit int64, timeout time.Duration, w io.Writer) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Kordo/"+appVersion)
	if strings.HasPrefix(url, "https://api.github.com/") {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && url == latestRelease {
		return nil, errors.New("релизов пока нет")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	if resp.ContentLength > limit {
		return nil, errors.New("файл слишком большой")
	}
	r := io.LimitReader(resp.Body, limit+1)
	if w == nil {
		b, err := io.ReadAll(r)
		if err == nil && int64(len(b)) > limit {
			err = errors.New("ответ слишком большой")
		}
		return b, err
	}
	n, err := io.Copy(w, r)
	if err == nil && n > limit {
		err = errors.New("файл слишком большой")
	}
	return nil, err
}

func updateDir() string { return filepath.Join(os.TempDir(), "Kordo-update") }

// cleanupUpdates removes installers downloaded by InstallUpdate. Files from
// the last hour are kept: the installer may still be running when it starts
// the new window. Files in use cannot be deleted and are simply skipped.
func cleanupUpdates() {
	dir := updateDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	left := 0
	for _, e := range entries {
		fi, err := e.Info()
		if err == nil && time.Since(fi.ModTime()) > time.Hour {
			if os.Remove(filepath.Join(dir, e.Name())) == nil {
				continue
			}
		}
		left++
	}
	if left == 0 {
		_ = os.Remove(dir)
	}
}
