package config

import (
	"maunium.net/go/mautrix/bridge/bridgeconfig"
	"maunium.net/go/mautrix/id"
)

type Config struct {
	*bridgeconfig.BaseConfig `yaml:",inline"`

	QQ struct {
		Protocol int `yaml:"protocol"`

		SignConfig struct {
			Server          string `yaml:"server"`
			Bearer          string `yaml:"bearer"`
			Key             string `yaml:"key"`
			IsBelow110      bool   `yaml:"is_below_110"`
			RefreshInterval int    `yaml:"refresh_interval"`
		} `yaml:"sign"`
	} `yaml:"qq"`

	Bridge BridgeConfig `yaml:"bridge"`
}

func (c *Config) CanAutoDoublePuppet(userID id.UserID) bool {
	_, homeserver, _ := userID.Parse()
	_, hasSecret := c.Bridge.LoginSharedSecretMap[homeserver]

	return hasSecret
}
