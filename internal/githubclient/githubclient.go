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
	"sort"
	"strings"
	"time"

	"github.com/andydixon/nexus/internal/models"
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
	FullName    string    `json:"full_name"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	HTMLURL     string    `json:"html_url"`
	Private     bool      `json:"private"`
	Fork        bool      `json:"fork"`
	Archived    bool      `json:"archived"`
	Language    string    `json:"language"`
	Stars       int       `json:"stargazers_count"`
	PushedAt    time.Time `json:"pushed_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Owner       struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// FetchRepos retrieves all repositories visible to the authenticated user,
// paginating through the API. Results are sorted by pushed_at descending
// (most recently active first).
func (c *Client) FetchRepos(ctx context.Context) ([]models.GitHubRepo, error) {
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

		body, readErr := io.ReadAll(resp.Body)
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
			FullName:    r.FullName,
			Name:        r.Name,
			Owner:       r.Owner.Login,
			Description: r.Description,
			HTMLURL:     r.HTMLURL,
			Private:     r.Private,
			Fork:        r.Fork,
			Archived:    r.Archived,
			Language:    r.Language,
			Stars:       r.Stars,
			PushedAt:    r.PushedAt.Format(time.RFC3339),
			UpdatedAt:   r.UpdatedAt.Format(time.RFC3339),
		}
	}
	return result, nil
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
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", fmt.Errorf("decoding user: %w", err)
	}
	return user.Login, nil
}
