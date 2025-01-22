package qqid

import (
	"github.com/LagrangeDev/LagrangeGo/client/auth"
	"go.mau.fi/util/jsontime"
)

type UserLoginMetadata struct {
	Device *auth.DeviceInfo `json:"device"`
	Token  []byte           `json:"token"`
}

type GhostMetadata struct {
	LastSync jsontime.Unix `json:"last_sync,omitempty"`
}

type PortalMetadata struct {
	ChatType ChatType      `json:"chat_type"`
	LastSync jsontime.Unix `json:"last_sync,omitempty"`
}
