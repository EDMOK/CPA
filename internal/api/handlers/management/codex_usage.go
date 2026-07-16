package management

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const (
	codexUsageCacheTTL         = time.Minute
	codexUsageMaxConcurrent    = 4
	codexUsageRequestUserAgent = "codex-tui/0.135.0 (Mac OS 26.5.0; arm64) iTerm.app/3.6.10 (codex-tui; 0.135.0)"
)

var codexUsageURL = "https://chatgpt.com/backend-api/wham/usage"

type codexUsageOverviewResponse struct {
	Accounts  []codexUsageAccount `json:"accounts"`
	Cached    bool                `json:"cached"`
	UpdatedAt time.Time           `json:"updated_at"`
}

type codexUsageAccount struct {
	AuthIndex string          `json:"auth_index"`
	Name      string          `json:"name"`
	Email     string          `json:"email,omitempty"`
	AccountID string          `json:"account_id,omitempty"`
	PlanType  string          `json:"plan_type,omitempty"`
	Usage     json.RawMessage `json:"usage,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// GetCodexUsageOverview retrieves the current usage windows for every enabled Codex OAuth credential.
func (h *Handler) GetCodexUsageOverview(c *gin.Context) {
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}

	refresh := strings.EqualFold(strings.TrimSpace(c.Query("refresh")), "true")
	if !refresh {
		if cached, ok := h.cachedCodexUsageOverview(); ok {
			cached.Cached = true
			c.JSON(http.StatusOK, cached)
			return
		}
	}

	accounts := h.loadCodexUsage(c.Request.Context())
	response := codexUsageOverviewResponse{Accounts: accounts, UpdatedAt: time.Now().UTC()}
	h.cacheCodexUsageOverview(response)
	c.JSON(http.StatusOK, response)
}

func (h *Handler) cachedCodexUsageOverview() (codexUsageOverviewResponse, bool) {
	h.codexUsageCacheMu.Lock()
	defer h.codexUsageCacheMu.Unlock()
	if h.codexUsageCacheExpiresAt.IsZero() || time.Now().After(h.codexUsageCacheExpiresAt) {
		return codexUsageOverviewResponse{}, false
	}
	return h.codexUsageCache, true
}

func (h *Handler) cacheCodexUsageOverview(response codexUsageOverviewResponse) {
	h.codexUsageCacheMu.Lock()
	defer h.codexUsageCacheMu.Unlock()
	h.codexUsageCache = response
	h.codexUsageCacheExpiresAt = time.Now().Add(codexUsageCacheTTL)
}

func (h *Handler) loadCodexUsage(ctx context.Context) []codexUsageAccount {
	auths := h.authManager.List()
	codexAuths := make([]*coreauth.Auth, 0, len(auths))
	for _, auth := range auths {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") || auth.Disabled || auth.Status == coreauth.StatusDisabled {
			continue
		}
		codexAuths = append(codexAuths, auth)
	}

	results := make([]codexUsageAccount, 0, len(codexAuths))
	jobs := make(chan *coreauth.Auth)
	var mu sync.Mutex
	var workers sync.WaitGroup
	workerCount := min(codexUsageMaxConcurrent, len(codexAuths))

	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for auth := range jobs {
				account := h.loadCodexAuthUsage(ctx, auth)
				mu.Lock()
				results = append(results, account)
				mu.Unlock()
			}
		}()
	}

	for _, auth := range codexAuths {
		jobs <- auth
	}
	close(jobs)
	workers.Wait()

	sort.Slice(results, func(i, j int) bool {
		return strings.ToLower(results[i].Name) < strings.ToLower(results[j].Name)
	})
	return results
}

func (h *Handler) loadCodexAuthUsage(ctx context.Context, auth *coreauth.Auth) codexUsageAccount {
	auth.EnsureIndex()
	account := codexUsageAccount{
		AuthIndex: auth.Index,
		Name:      auth.FileName,
		Email:     authEmail(auth),
		AccountID: authProjectID(auth),
	}
	if account.Name == "" {
		account.Name = auth.ID
	}
	if claims := extractCodexIDTokenClaims(auth); claims != nil {
		if value, ok := claims["plan_type"].(string); ok {
			account.PlanType = value
		}
		if account.AccountID == "" {
			if value, ok := claims["chatgpt_account_id"].(string); ok {
				account.AccountID = value
			}
		}
	}
	if auth.Metadata != nil {
		if account.AccountID == "" {
			if value, ok := auth.Metadata["account_id"].(string); ok {
				account.AccountID = strings.TrimSpace(value)
			}
		}
	}

	token, errToken := h.resolveTokenForAuth(ctx, auth)
	if errToken != nil {
		account.Error = "unable to refresh credential"
		return account
	}
	if token == "" {
		account.Error = "credential token unavailable"
		return account
	}

	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, codexUsageURL, nil)
	if errRequest != nil {
		account.Error = "unable to build usage request"
		return account
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Originator", "codex-tui")
	req.Header.Set("User-Agent", codexUsageRequestUserAgent)
	if account.AccountID != "" {
		req.Header.Set("Chatgpt-Account-Id", account.AccountID)
	}
	util.ApplyCustomHeadersFromAttrs(req, auth.Attributes)

	client := &http.Client{Transport: h.apiCallTransport(auth)}
	resp, errDo := client.Do(req)
	if errDo != nil {
		account.Error = "usage request failed"
		return account
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		account.Error = "unable to read usage response"
		return account
	}
	if resp.StatusCode != http.StatusOK {
		account.Error = fmt.Sprintf("usage request returned HTTP %d", resp.StatusCode)
		return account
	}
	if !json.Valid(body) {
		account.Error = "usage response was not JSON"
		return account
	}
	account.Usage = append(account.Usage[:0], body...)
	return account
}

