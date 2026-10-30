package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PeaChack/Kordo/core/version"
)

// AppVersion is set at build time (-X github.com/PeaChack/Kordo/mobile.AppVersion=...).
var AppVersion = "dev"

const latestRelease = "https://api.github.com/repos/PeaChack/Kordo/releases/latest"

type updateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	Page      string `json:"page,omitempty"`
	APK       string `json:"apk,omitempty"`    // download URL
	SHA256    string `json:"sha256,omitempty"` // expected hash of the APK
}

// CheckUpdate asks GitHub for the latest release and returns JSON
// {current, latest, available, page, apk, sha256}. Dev builds never offer
// updates. The app downloads the APK, checks SHA-256 and asks Android to
// install it (Android itself refuses an APK signed with another key).
func CheckUpdate() (string, error) {
	info := updateInfo{Current: AppVersion}
	if !version.Valid(AppVersion) {
		return toJSON(info), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body, err := get(ctx, latestRelease, 1<<20)
	if err != nil {
		return "", fmt.Errorf("проверка обновлений: %w", err)
	}
	var rel struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", err
	}
	if rel.Draft || rel.Prerelease || rel.TagName == "" {
		return toJSON(info), nil
	}
	info.Latest = strings.TrimPrefix(rel.TagName, "v")
	info.Page = rel.HTMLURL
	var sumURL string
	for _, a := range rel.Assets {
		n := strings.ToLower(a.Name)
		switch {
		case strings.HasPrefix(n, "kordo-") && strings.HasSuffix(n, ".apk"):
			info.APK = a.URL
		case strings.HasPrefix(n, "kordo-") && strings.HasSuffix(n, ".apk.sha256"):
			sumURL = a.URL
		}
	}
	if info.APK == "" || sumURL == "" || version.Compare(info.Latest, AppVersion) <= 0 {
		return toJSON(info), nil
	}
	sum, err := get(ctx, sumURL, 4096)
	if err != nil {
		return "", fmt.Errorf("загрузка контрольной суммы: %w", err)
	}
	if f := strings.Fields(string(sum)); len(f) > 0 && len(f[0]) == 64 {
		info.SHA256 = strings.ToLower(f[0])
		info.Available = true
	}
	return toJSON(info), nil
}

func get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Kordo-Android/"+AppVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("релизов пока нет")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}
