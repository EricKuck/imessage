package connector

import (
	"maunium.net/go/mautrix/bridgev2/status"
)

const (
	IMNotLoggedIn      status.BridgeStateErrorCode = "im-not-logged-in"
	IMBadCredentials   status.BridgeStateErrorCode = "im-bad-credentials"
	IMConnectionFailed status.BridgeStateErrorCode = "im-connection-failed"
	IMDisconnected     status.BridgeStateErrorCode = "im-disconnected"
)

func init() {
	status.BridgeStateHumanErrors.Update(status.BridgeStateErrorMap{
		IMNotLoggedIn:      "Not logged in to BlueBubbles, please log in again",
		IMBadCredentials:   "BlueBubbles rejected the password, please log in again",
		IMConnectionFailed: "Failed to connect to the BlueBubbles server",
		IMDisconnected:     "Disconnected from the BlueBubbles server, the bridge will keep retrying",
	})
}
