// Package githubclient provides a lightweight GitHub REST API client for
// fetching repository metadata. It supports both github.com and GitHub
// Enterprise instances via a configurable base URL.
package githubclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/andydixon/nexus/internal/models"
)

const (
	maxGitHubJSONBodyBytes  = 4 << 20
	maxGitHubErrorBodyBytes = 64 << 10
)

// Client wraps the GitHub REST API for repository listing.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// New creates a Client for the given API base URL and personal access token.
// baseURL should be the API root, e.g. "https://api.github.com" or
// "https://ghe.example.com/api/v3".
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// apiRepo is the subset of the GitHub API response we care about.
type apiRepo struct {
	FullName      string    `json:"full_name"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	HTMLURL       string    `json:"html_url"`
	DefaultBranch string    `json:"default_branch"`
	Private       bool      `json:"private"`
	Fork          bool      `json:"fork"`
	Archived      bool      `json:"archived"`
	Language      string    `json:"language"`
	Stars         int       `json:"stargazers_count"`
	PushedAt      time.Time `json:"pushed_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// FetchRepos retrieves all repositories visible to the authenticated user,
// paginating through the API. Results are sorted by pushed_at descending
// (most recently active first).
func (c *Client) FetchRepos(ctx context.Context) ([]models.GitHubRepo, error) {
	return c.fetchRepos(ctx, nil)
}

// FetchReposWithProgress fetches repositories and reports enrichment progress.
func (c *Client) FetchReposWithProgress(ctx context.Context, progress func(done int, total int)) ([]models.GitHubRepo, error) {
	return c.fetchRepos(ctx, progress)
}

func (c *Client) fetchRepos(ctx context.Context, progress func(done int, total int)) ([]models.GitHubRepo, error) {
	var all []apiRepo

	page := 1
	for {
		url := fmt.Sprintf("%s/user/repos?per_page=100&page=%d&sort=pushed&direction=desc", c.baseURL, page)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("building request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "NexusTerminal/1.0")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetching repos page %d: %w", page, err)
		}

		body, readErr := readLimitedGitHubBody(resp.Body, maxGitHubJSONBodyBytes)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("reading response body: %w", readErr)
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("authentication failed (HTTP %d) — check your access token", resp.StatusCode)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GitHub API returned HTTP %d: %s", resp.StatusCode, string(body))
		}

		var batch []apiRepo
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, fmt.Errorf("decoding repos: %w", err)
		}

		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)

		// Safety cap — 5000 repos should be more than enough.
		if len(all) >= 5000 || len(batch) < 100 {
			break
		}
		page++
	}

	// Sort by pushed_at descending (newest first).
	sort.Slice(all, func(i, j int) bool {
		return all[i].PushedAt.After(all[j].PushedAt)
	})

	result := make([]models.GitHubRepo, len(all))
	for i, r := range all {
		result[i] = models.GitHubRepo{
			FullName:      r.FullName,
			Name:          r.Name,
			Owner:         r.Owner.Login,
			Description:   r.Description,
			HTMLURL:       r.HTMLURL,
			DefaultBranch: r.DefaultBranch,
			Private:       r.Private,
			Fork:          r.Fork,
			Archived:      r.Archived,
			Language:      r.Language,
			Stars:         r.Stars,
			PushedAt:      r.PushedAt.Format(time.RFC3339),
			UpdatedAt:     r.UpdatedAt.Format(time.RFC3339),
		}
	}

	if progress != nil {
		progress(0, len(result))
	}

	// Enrich with latest commit metadata (best-effort).
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	var done int32
	for i := range result {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ref := result[idx].DefaultBranch
			if ref == "" {
				ref = "HEAD"
			}
			committer, committedAt, err := c.FetchLatestCommit(ctx, result[idx].FullName, ref)
			if err != nil {
				if progress != nil {
					current := int(atomic.AddInt32(&done, 1))
					progress(current, len(result))
				}
				return
			}
			result[idx].LatestCommitter = committer
			result[idx].LatestCommitAt = committedAt
			if progress != nil {
				current := int(atomic.AddInt32(&done, 1))
				progress(current, len(result))
			}
		}(i)
	}
	wg.Wait()
	return result, nil
}

// FetchLatestCommit returns the committer name and commit time for a repo/ref.
func (c *Client) FetchLatestCommit(ctx context.Context, fullName string, ref string) (string, string, error) {
	url := fmt.Sprintf("%s/repos/%s/commits/%s", c.baseURL, fullName, ref)
	if ref == "" {
		url = fmt.Sprintf("%s/repos/%s/commits/HEAD", c.baseURL, fullName)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NexusTerminal/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetching commit: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", "", fmt.Errorf("authentication failed (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GitHub API returned HTTP %d: %s", resp.StatusCode, readGitHubErrorBody(resp.Body))
	}

	var payload struct {
		Commit struct {
			Committer struct {
				Name string    `json:"name"`
				Date time.Time `json:"date"`
			} `json:"committer"`
			Author struct {
				Name string    `json:"name"`
				Date time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}

	body, err := readLimitedGitHubBody(resp.Body, maxGitHubJSONBodyBytes)
	if err != nil {
		return "", "", fmt.Errorf("reading commit response: %w", err)
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", fmt.Errorf("decoding commit: %w", err)
	}

	name := payload.Commit.Committer.Name
	date := payload.Commit.Committer.Date
	if name == "" {
		name = payload.Commit.Author.Name
		date = payload.Commit.Author.Date
	}
	if name == "" || date.IsZero() {
		return "", "", fmt.Errorf("commit metadata missing")
	}

	return name, date.Format(time.RFC3339), nil
}

// DownloadRepoArchive downloads a repository zip archive to the given path.
func (c *Client) DownloadRepoArchive(ctx context.Context, fullName string, ref string, destPath string) error {
	if fullName == "" {
		return fmt.Errorf("repository name cannot be empty")
	}
	url := fmt.Sprintf("%s/repos/%s/zipball", c.baseURL, fullName)
	if ref != "" {
		url = url + "/" + ref
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NexusTerminal/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("authentication failed (HTTP %d) — check your access token", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned HTTP %d: %s", resp.StatusCode, readGitHubErrorBody(resp.Body))
	}

	file, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return fmt.Errorf("writing archive: %w", err)
	}
	return nil
}

// ValidateToken makes a lightweight call to /user to confirm the token works.
func (c *Client) ValidateToken(ctx context.Context) (string, error) {
	url := c.baseURL + "/user"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NexusTerminal/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("contacting GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token validation failed (HTTP %d)", resp.StatusCode)
	}

	var user struct {
		Login string `json:"login"`
	}
	body, err := readLimitedGitHubBody(resp.Body, maxGitHubJSONBodyBytes)
	if err != nil {
		return "", fmt.Errorf("reading user response: %w", err)
	}
	if err := json.Unmarshal(body, &user); err != nil {
		return "", fmt.Errorf("decoding user: %w", err)
	}
	return user.Login, nil
}

func readLimitedGitHubBody(r io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxBytes)
	}
	return body, nil
}

func readGitHubErrorBody(r io.Reader) string {
	body, err := readLimitedGitHubBody(r, maxGitHubErrorBodyBytes)
	if err != nil {
		return "<response body unavailable: " + err.Error() + ">"
	}
	return strings.TrimSpace(string(body))
}
