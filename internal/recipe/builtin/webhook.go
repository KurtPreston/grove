package builtin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"grove/internal/project"
	"grove/internal/recipe"
	"grove/internal/ui"
)

func init() { recipe.Register("webhook", webhookRecipe) }

// webhookRecipe POSTs the workspace descriptor to GROVE_WEBHOOK_URL. For the
// remote (SSH) flow, point GROVE_WEBHOOK_URL at the reverse-tunnel endpoint,
// e.g. http://127.0.0.1:39787/open, which forwards to docent on your workstation.
func webhookRecipe(ctx recipe.Context) error {
	if ctx.WebhookURL == "" {
		ui.Warn("webhook: GROVE_WEBHOOK_URL is not set; skipping.")
		return nil
	}
	if ctx.SSHHost == "" {
		ui.Warn("webhook: GROVE_SSH_HOST not set; docent won't know which host to open.")
	}
	return postWorkspace(ctx.WebhookURL, ctx)
}

// payload is the loose contract shared with docent: {host, path, name}.
type payload struct {
	Host string `json:"host"`
	Path string `json:"path"`
	Name string `json:"name"`
}

// postWorkspace sends {host, path, name} as JSON to url.
func postWorkspace(url string, ctx recipe.Context) error {
	body, err := json.Marshal(payload{
		Host: ctx.SSHHost,
		Path: ctx.Dir,
		Name: project.Sanitize(ctx.Branch),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if ctx.WebhookToken != "" {
		req.Header.Set("Authorization", "Bearer "+ctx.WebhookToken)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	ui.Info("webhook delivered to " + url)
	return nil
}
