package main

import (
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"

	"go.mau.fi/mautrix-imessage/pkg/connector"
)

var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

var m = mxmain.BridgeMain{
	Name:        "mautrix-imessage",
	Description: "A Matrix-iMessage puppeting bridge using BlueBubbles",
	URL:         "https://github.com/EricKuck/imessage",
	Version:     "0.2.0",
	Connector:   &connector.IMConnector{},
}

func main() {
	m.InitVersion(Tag, Commit, BuildTime)
	m.Run()
}
