package deliverytext

// DeliveryWarning explains the reported Codex delivery limitation and its recovery.
func DeliveryWarning(reason string) string {
	if reason != "codex_no_daemon" && reason != "codex_no_daemon_orca" {
		return ""
	}
	text := "Codex is running with --no-daemon, so Aboard can't deliver to it. Restart Codex without --no-daemon."
	if reason == "codex_no_daemon_orca" {
		text += " Orca adds --no-daemon; set export ORCA_CODEX_ISOLATE=0 in ~/.zshrc (or run command codex), then restart Codex."
	}
	return text
}
