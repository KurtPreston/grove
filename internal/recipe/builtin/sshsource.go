package builtin

import (
	"grove/internal/recipe"
	"grove/internal/ui"
)

func init() { recipe.Register("ssh-source-webhook", sshSourceRecipe) }

// sshSourceRecipe fires the workspace webhook back to the machine you SSH'd in
// from. It targets 127.0.0.1:<port>, which a reverse SSH tunnel
// (RemoteForward <port> 127.0.0.1:<port>) forwards to docent on your workstation.
// No-ops when not inside an SSH session.
func sshSourceRecipe(ctx recipe.Context) error {
	if !ctx.InSSH {
		ui.Info("ssh-source-webhook: not in an SSH session; skipping.")
		return nil
	}
	port := ctx.WebhookPort
	if port == "" {
		port = "39787"
	}
	if ctx.SSHHost == "" {
		ui.Warn("ssh-source-webhook: GROVE_SSH_HOST not set; docent won't know which host to open.")
	}
	url := "http://127.0.0.1:" + port + "/open"
	return postWorkspace(url, ctx)
}
