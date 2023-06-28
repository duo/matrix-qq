package config

import (
	"maunium.net/go/mautrix/bridge/bridgeconfig"
	"maunium.net/go/mautrix/id"
)

type Config struct {
	*bridgeconfig.BaseConfig `yaml:",inline"`

	QQ struct {
		Protocol   int    `yaml:"protocol"`
		SignServer string `yaml:"sign_server"`
	} `yaml:"qq"`

	Bridge BridgeConfig `yaml:"bridge"`
}

func (c *Config) CanAutoDoublePuppet(userID id.UserID) bool {
	_, homeserver, _ := userID.Parse()
	_, hasSecret := c.Bridge.LoginSharedSecretMap[homeserver]

	return hasSecret
}
