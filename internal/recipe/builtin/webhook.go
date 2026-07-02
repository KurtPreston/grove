package builtin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"grove/internal/config"
	"grove/internal/recipe"
	"grove/internal/ui"
)

func init() { recipe.Register("webhook", webhookRecipe) }

// webhookRecipe POSTs the recipe's params (with env vars expanded in string
// leaves) as JSON to the recipe's url. For the remote (SSH) flow, point url at
// the reverse-tunnel endpoint, e.g. http://127.0.0.1:39788/open.
func webhookRecipe(ctx recipe.Context, rc config.RecipeConfig) error {
	if rc.URL == "" {
		ui.Warn("webhook: recipe \"url\" is not set; skipping.")
		return nil
	}
	return postJSON(rc, ctx)
}

func postJSON(rc config.RecipeConfig, ctx recipe.Context) error {
	env := ctx.Env()
	url := recipe.ExpandString(rc.URL, env)
	token := recipe.ExpandString(rc.Token, env)

	params := rc.Params
	if params == nil {
		params = map[string]any{}
	}
	body, err := json.Marshal(recipe.ExpandValue(params, env))
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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
