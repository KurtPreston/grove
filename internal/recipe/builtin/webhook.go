package builtin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"grove/internal/config"
	"grove/internal/project"
	"grove/internal/recipe"
	"grove/internal/ui"
)

func init() { recipe.Register("webhook", webhookRecipe) }

// webhookRecipe POSTs the workspace descriptor to the recipe's url. For the
// remote (SSH) flow, point url at the reverse-tunnel endpoint, e.g.
// http://127.0.0.1:39787/open, which forwards to docent on your workstation.
func webhookRecipe(ctx recipe.Context, rc config.RecipeConfig) error {
	if rc.URL == "" {
		ui.Warn("webhook: recipe \"url\" is not set; skipping.")
		return nil
	}
	if rc.SSHHost == "" {
		ui.Warn("webhook: recipe \"sshHost\" not set; docent won't know which host to open.")
	}
	return postWorkspace(rc, ctx)
}

// payload is the loose contract shared with docent: {host, path, name}.
type payload struct {
	Host string `json:"host"`
	Path string `json:"path"`
	Name string `json:"name"`
}

// postWorkspace sends {host, path, name} as JSON to the recipe's url.
func postWorkspace(rc config.RecipeConfig, ctx recipe.Context) error {
	body, err := json.Marshal(payload{
		Host: rc.SSHHost,
		Path: ctx.Dir,
		Name: project.Sanitize(ctx.Branch),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, rc.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if rc.Token != "" {
		req.Header.Set("Authorization", "Bearer "+rc.Token)
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
	ui.Info("webhook delivered to " + rc.URL)
	return nil
}
