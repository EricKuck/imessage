package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-imessage/pkg/bluebubbles"
)

type IMConnector struct {
	br     *bridgev2.Bridge
	Config Config
}

var (
	_ bridgev2.NetworkConnector               = (*IMConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*IMConnector)(nil)
	_ bridgev2.IdentifierValidatingNetwork    = (*IMConnector)(nil)
)

func (ic *IMConnector) Init(bridge *bridgev2.Bridge) {
	ic.br = bridge
}

func (ic *IMConnector) Start(ctx context.Context) error {
	return nil
}

func (ic *IMConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:          "iMessage",
		NetworkURL:           "https://support.apple.com/messages",
		NetworkIcon:          "mxc://maunium.net/tManJEpANASZvDVzvRvhILdX",
		NetworkID:            "imessage",
		BeeperBridgeType:     "go.mau.fi/mautrix-imessage",
		DefaultPort:          29332,
		DefaultCommandPrefix: "!im",
	}
}

func (ic *IMConnector) FillPortalBridgeInfo(portal *bridgev2.Portal, content *event.BridgeEventContent) {
	meta, ok := portal.Metadata.(*PortalMetadata)
	if !ok {
		return
	}
	switch meta.Service {
	case bluebubbles.ServiceSMS:
		content.Protocol.ID = "imessage-sms"
		content.Protocol.DisplayName = "iMessage (SMS)"
	case bluebubbles.ServiceIMessage:
		content.Protocol.ID = "imessage"
		content.Protocol.DisplayName = "iMessage"
	}
}

func (ic *IMConnector) ValidateUserID(id networkid.UserID) bool {
	return isPhoneNumber(string(id)) || isEmail(string(id))
}
