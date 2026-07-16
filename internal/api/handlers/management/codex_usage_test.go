package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestGetCodexUsageOverviewListsEnabledCodexAuths(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")

	var requests int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Authorization"); got != "Bearer codex-access-token" {
			t.Fatalf("Authorization = %q, want Codex access token", got)
		}
		if got := r.Header.Get("Chatgpt-Account-Id"); got != "account-1" {
			t.Fatalf("Chatgpt-Account-Id = %q, want account-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":42,"reset_at":1234567890}}}`))
	}))
	defer upstream.Close()

	previousURL := codexUsageURL
	codexUsageURL = upstream.URL
	t.Cleanup(func() { codexUsageURL = previousURL })

	manager := coreauth.NewManager(nil, nil, nil)
	codexAuth := &coreauth.Auth{
		ID:       "codex-auth",
		FileName: "codex.json",
		Provider: "codex",
		Metadata: map[string]any{
			"access_token": "codex-access-token",
			"account_id":   "account-1",
			"email":        "codex@example.com",
		},
	}
	if _, errRegister := manager.Register(context.Background(), codexAuth); errRegister != nil {
		t.Fatalf("register Codex auth: %v", errRegister)
	}
	for _, auth := range []*coreauth.Auth{
		{ID: "gemini-auth", Provider: "gemini", Metadata: map[string]any{"access_token": "ignored"}},
		{ID: "disabled-codex", Provider: "codex", Disabled: true, Metadata: map[string]any{"access_token": "ignored"}},
	} {
		if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
			t.Fatalf("register auth: %v", errRegister)
		}
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/codex-usage-overview", nil)
	h.GetCodexUsageOverview(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if requests != 1 {
		t.Fatalf("upstream request count = %d, want 1", requests)
	}

	var response codexUsageOverviewResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &response); errDecode != nil {
		t.Fatalf("decode response: %v", errDecode)
	}
	if len(response.Accounts) != 1 {
		t.Fatalf("account count = %d, want 1", len(response.Accounts))
	}
	account := response.Accounts[0]
	if account.AuthIndex != codexAuth.EnsureIndex() || account.Name != "codex.json" || account.Email != "codex@example.com" || account.AccountID != "account-1" {
		t.Fatalf("account = %+v, want Codex credential metadata", account)
	}
	if string(account.Usage) != `{"rate_limit":{"primary_window":{"used_percent":42,"reset_at":1234567890}}}` {
		t.Fatalf("usage = %s, want upstream payload", account.Usage)
	}
}

func TestGetCodexUsageOverviewCachesResult(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")

	var requests int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"rate_limit":{}}`))
	}))
	defer upstream.Close()

	previousURL := codexUsageURL
	codexUsageURL = upstream.URL
	t.Cleanup(func() { codexUsageURL = previousURL })

	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "codex-auth", Provider: "codex", Metadata: map[string]any{"access_token": "token"}}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	for attempt := 0; attempt < 2; attempt++ {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/codex-usage-overview", nil)
		h.GetCodexUsageOverview(ctx)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want %d", attempt, rec.Code, http.StatusOK)
		}
		if attempt == 1 {
			var response codexUsageOverviewResponse
			if errDecode := json.Unmarshal(rec.Body.Bytes(), &response); errDecode != nil {
				t.Fatalf("decode cached response: %v", errDecode)
			}
			if !response.Cached {
				t.Fatal("expected second response to be cached")
			}
		}
	}
	if requests != 1 {
		t.Fatalf("upstream request count = %d, want 1", requests)
	}
}
