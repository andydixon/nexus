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
	"net/http"
	"strings"
	"time"
)

const (
	// defaultOwner and defaultRepo identify the GitHub repository to check.
	// These should be overridden via SetRepository if the project is hosted
	// elsewhere.
	defaultOwner = "nexus-terminal"
	defaultRepo  = "nexus-terminal"

	// requestTimeout limits the HTTP request duration.
	requestTimeout = 10 * time.Second
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
	req.Header.Set("User-Agent", "NexusTerminal-UpdateChecker")

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

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
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

// normaliseVersion strips a leading "v" and trims whitespace.
func normaliseVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	return v
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
