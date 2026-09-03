// Package updater checks for new application releases by querying the
// GitHub Releases API. It compares the latest published tag against the
// current build version and reports whether an update is available.
//
// The check is non-blocking and opt-in — it runs in the background and
// does not download or install anything automatically.
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// defaultOwner and defaultRepo identify the GitHub repository to check.
	// These should be overridden via SetRepository if the project is hosted
	// elsewhere.
	defaultOwner = "alchementary-link"
	defaultRepo  = "alchementary-link"

	// requestTimeout limits the HTTP request duration.
	requestTimeout = 10 * time.Second

	// maxUpdateResponseBytes bounds release and manifest responses before
	// parsing them into memory.
	maxUpdateResponseBytes = 1 << 20
)

// Release describes a GitHub release with the fields we care about.
type Release struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	Prerelease  bool   `json:"prerelease"`
	Draft       bool   `json:"draft"`
}

// UpdateInfo carries the result of an update check.
type UpdateInfo struct {
	Available      bool   `json:"available"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	ReleaseURL     string `json:"releaseURL"`
	ReleaseName    string `json:"releaseName"`
	ReleaseNotes   string `json:"releaseNotes"`
	PublishedAt    string `json:"publishedAt"`
}

// Checker performs version checks against GitHub Releases.
type Checker struct {
	owner   string
	repo    string
	current string
	client  *http.Client
}

// Manifest describes a self-hosted update manifest.
type Manifest struct {
	LatestVersion string `json:"latestVersion"`
	Version       string `json:"version"`
	ReleaseURL    string `json:"releaseURL"`
	ReleaseName   string `json:"releaseName"`
	ReleaseNotes  string `json:"releaseNotes"`
	PublishedAt   string `json:"publishedAt"`
}

// URLChecker performs version checks against a self-hosted update manifest.
type URLChecker struct {
	url     string
	current string
	client  *http.Client
}

// NewChecker creates an update checker for the given current version.
// The version string should be a semver tag (e.g. "v1.2.3" or "1.2.3").
func NewChecker(currentVersion string) *Checker {
	return &Checker{
		owner:   defaultOwner,
		repo:    defaultRepo,
		current: normaliseVersion(currentVersion),
		client: &http.Client{
			Timeout: requestTimeout,
		},
	}
}

// NewURLChecker creates a checker for a self-hosted update manifest URL.
func NewURLChecker(url string, currentVersion string) *URLChecker {
	return &URLChecker{
		url:     url,
		current: normaliseVersion(currentVersion),
		client: &http.Client{
			Timeout: requestTimeout,
		},
	}
}

// SetRepository overrides the GitHub repository to check.
func (c *Checker) SetRepository(owner, repo string) {
	c.owner = owner
	c.repo = repo
}

// Check queries the GitHub API for the latest release and compares it to
// the current version. Returns UpdateInfo describing the result.
func (c *Checker) Check(ctx context.Context) (*UpdateInfo, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", c.owner, c.repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("updater: failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "AlchementaryLink-UpdateChecker")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("updater: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// No releases exist yet — not an error.
		return &UpdateInfo{
			Available:      false,
			CurrentVersion: c.current,
			LatestVersion:  c.current,
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("updater: GitHub API returned %d", resp.StatusCode)
	}

	body, err := readLimitedUpdateBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("updater: failed to read response: %w", err)
	}

	var release Release
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("updater: failed to decode response: %w", err)
	}

	// Skip drafts and pre-releases.
	if release.Draft || release.Prerelease {
		return &UpdateInfo{
			Available:      false,
			CurrentVersion: c.current,
			LatestVersion:  normaliseVersion(release.TagName),
		}, nil
	}

	latest := normaliseVersion(release.TagName)
	available := isNewer(latest, c.current)

	return &UpdateInfo{
		Available:      available,
		CurrentVersion: c.current,
		LatestVersion:  latest,
		ReleaseURL:     release.HTMLURL,
		ReleaseName:    release.Name,
		ReleaseNotes:   truncateNotes(release.Body, 500),
		PublishedAt:    release.PublishedAt,
	}, nil
}

// Check queries the update manifest URL and compares it to the current version.
func (c *URLChecker) Check(ctx context.Context) (*UpdateInfo, error) {
	if c.url == "" {
		return nil, fmt.Errorf("updater: update manifest URL is empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, fmt.Errorf("updater: failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json, text/plain")
	req.Header.Set("User-Agent", "AlchementaryLink-UpdateChecker")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("updater: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("updater: update server returned %d", resp.StatusCode)
	}

	body, err := readLimitedUpdateBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("updater: failed to read response: %w", err)
	}

	info, ok := parseManifest(body, c.current)
	if ok {
		return info, nil
	}

	latest := normaliseVersion(strings.TrimSpace(string(body)))
	if latest == "" {
		return nil, fmt.Errorf("updater: update manifest is empty")
	}

	return &UpdateInfo{
		Available:      isNewer(latest, c.current),
		CurrentVersion: c.current,
		LatestVersion:  latest,
	}, nil
}

// normaliseVersion strips a leading "v" and trims whitespace.
func normaliseVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	return v
}

func readLimitedUpdateBody(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxUpdateResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxUpdateResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxUpdateResponseBytes)
	}
	return body, nil
}

func parseManifest(body []byte, current string) (*UpdateInfo, bool) {
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, false
	}

	latest := manifest.LatestVersion
	if latest == "" {
		latest = manifest.Version
	}
	latest = normaliseVersion(latest)
	if latest == "" {
		return nil, false
	}

	return &UpdateInfo{
		Available:      isNewer(latest, current),
		CurrentVersion: current,
		LatestVersion:  latest,
		ReleaseURL:     manifest.ReleaseURL,
		ReleaseName:    manifest.ReleaseName,
		ReleaseNotes:   manifest.ReleaseNotes,
		PublishedAt:    manifest.PublishedAt,
	}, true
}

// isNewer performs a simple string comparison of dot-separated version parts.
// Returns true if latest > current (basic semver, no pre-release handling).
func isNewer(latest, current string) bool {
	if current == "" || current == "dev" {
		return false // dev builds never trigger update prompts
	}

	lParts := strings.Split(latest, ".")
	cParts := strings.Split(current, ".")

	maxLen := len(lParts)
	if len(cParts) > maxLen {
		maxLen = len(cParts)
	}

	for i := 0; i < maxLen; i++ {
		var lv, cv int
		if i < len(lParts) {
			fmt.Sscanf(lParts[i], "%d", &lv)
		}
		if i < len(cParts) {
			fmt.Sscanf(cParts[i], "%d", &cv)
		}
		if lv > cv {
			return true
		}
		if lv < cv {
			return false
		}
	}
	return false
}

// truncateNotes trims release notes to a maximum length.
func truncateNotes(body string, maxLen int) string {
	if len(body) <= maxLen {
		return body
	}
	return body[:maxLen] + "…"
}
