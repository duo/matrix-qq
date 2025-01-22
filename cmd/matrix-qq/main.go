package main

import (
	"github.com/duo/matrix-qq/pkg/connector"

	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"
)

// Information to find out exactly which commit the bridge was built from.
// These are filled at build time with the -X linker flag.
var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	m := mxmain.BridgeMain{
		Name:        "mautrix-qq",
		URL:         "https://github.com/duo/matrix-qq",
		Description: "A Matrix-QQ puppeting bridge.",
		Version:     "0.2.1",
		Connector:   &connector.QQConnector{},
	}

	m.InitVersion(Tag, Commit, BuildTime)
	m.Run()
}
