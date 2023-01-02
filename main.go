package main

import (
	_ "embed"

	"github.com/duo/matrix-qq/internal"
	"github.com/duo/matrix-qq/internal/config"
	"maunium.net/go/mautrix/bridge"
	"maunium.net/go/mautrix/util/configupgrade"
)

// Information to find out exactly which commit the bridge was built from.
// These are filled at build time with the -X linker flag.
var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

//go:embed example-config.yaml
var exampleConfig string

func main() {
	br := internal.NewQQBridge(exampleConfig)
	br.Bridge = bridge.Bridge{
		Name:         "matrix-qq",
		URL:          "https://github.com/duo/matrix-qq",
		Description:  "A Matrix-QQ puppeting bridge.",
		Version:      "0.1.3",
		ProtocolName: "QQ",

		CryptoPickleKey: "github.com/duo/matrix-qq",

		ConfigUpgrader: &configupgrade.StructUpgrader{
			SimpleUpgrader: configupgrade.SimpleUpgrader(config.DoUpgrade),
			Blocks:         config.SpacedBlocks,
			Base:           exampleConfig,
		},

		Child: br,
	}
	br.InitVersion(Tag, Commit, BuildTime)

	br.Main()
}
