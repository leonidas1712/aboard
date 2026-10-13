package cli

import (
	"strings"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

func (a *app) currentDeliveryWarning() string {
	key, ok := a.sessionKey()
	if !ok || key.Harness != "codex" {
		return ""
	}
	return a.withSessionHome(delivery.Request{Harness: key.Harness, Session: key.ID}).DeliveryWarning
}

func statusWarningText(text string, reason *string) string {
	if reason == nil {
		return text
	}
	warning := deliverytext.DeliveryWarning(*reason)
	if warning != "" && !strings.Contains(text, warning) {
		text += "Delivery: " + warning + "\n"
	}
	return text
}
