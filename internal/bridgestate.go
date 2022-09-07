package internal

import (
	"maunium.net/go/mautrix/bridge/status"
)

const (
	QQLoggedOut        status.BridgeStateErrorCode = "qq-logged-out"
	QQNotConnected     status.BridgeStateErrorCode = "qq-not-connected"
	QQConnecting       status.BridgeStateErrorCode = "qq-connecting"
	QQConnectionFailed status.BridgeStateErrorCode = "qq-connection-failed"
)

func init() {
	status.BridgeStateHumanErrors.Update(status.BridgeStateErrorMap{
		QQLoggedOut:        "You were logged out from another device. Relogin to continue using the bridge.",
		QQNotConnected:     "You're not connected to QQ.",
		QQConnecting:       "Reconnecting to QQ...",
		QQConnectionFailed: "Connect to the QQ servers failed.",
	})
}

func (user *User) GetRemoteID() string {
	if user == nil || user.UID.IsEmpty() {
		return ""
	}

	return user.UID.String()
}

func (user *User) GetRemoteName() string {
	if user == nil || user.UID.IsEmpty() {
		return ""
	}

	return user.UID.Uin
}
