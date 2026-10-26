package connector

import (
	"context"
	"time"

	"go.mau.fi/util/ffmpeg"
	"go.mau.fi/util/jsontime"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

var generalCaps = &bridgev2.NetworkGeneralCapabilities{
	Provisioning: bridgev2.ProvisioningCapabilities{
		ResolveIdentifier: bridgev2.ResolveIdentifierCapabilities{
			CreateDM:    true,
			LookupPhone: true,
			LookupEmail: true,
			AnyPhone:    true,
			ContactList: true,
		},
	},
}

func (ic *IMConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return generalCaps
}

func (ic *IMConnector) GetBridgeInfoVersion() (info, caps int) {
	return 1, 1
}

const maxFileSize = 100 * 1024 * 1024

func supportedIfFFmpeg() event.CapabilitySupportLevel {
	if ffmpeg.Supported() {
		return event.CapLevelPartialSupport
	}
	return event.CapLevelRejected
}

func privateAPILevel(enabled bool) event.CapabilitySupportLevel {
	if enabled {
		return event.CapLevelFullySupported
	}
	return event.CapLevelRejected
}

func makeRoomFeatures(privateAPI bool) *event.RoomFeatures {
	id := "fi.mau.imessage.bluebubbles.capabilities.2026_10_26"
	if privateAPI {
		id += "+private_api"
	}
	if ffmpeg.Supported() {
		id += "+ffmpeg"
	}
	anyFile := func() *event.FileFeatures {
		return &event.FileFeatures{
			MimeTypes: map[string]event.CapabilitySupportLevel{"*/*": event.CapLevelFullySupported},
			// Captions are sent as a separate message.
			Caption: event.CapLevelPartialSupport,
			MaxSize: maxFileSize,
		}
	}
	return &event.RoomFeatures{
		ID: id,
		File: event.FileFeatureMap{
			event.MsgImage: anyFile(),
			event.MsgVideo: anyFile(),
			event.MsgAudio: anyFile(),
			event.MsgFile:  anyFile(),
			event.CapMsgGIF: {
				MimeTypes: map[string]event.CapabilitySupportLevel{"image/gif": event.CapLevelFullySupported},
				Caption:   event.CapLevelPartialSupport,
				MaxSize:   maxFileSize,
			},
			event.CapMsgVoice: {
				MimeTypes: map[string]event.CapabilitySupportLevel{
					"audio/ogg": supportedIfFFmpeg(),
					"audio/mp4": event.CapLevelFullySupported,
				},
				MaxSize: maxFileSize,
			},
		},
		Reply:               event.CapLevelFullySupported,
		Edit:                privateAPILevel(privateAPI),
		EditMaxAge:          ptr.Ptr(jsontime.S(15 * time.Minute)),
		EditMaxCount:        5,
		Delete:              privateAPILevel(privateAPI),
		DeleteMaxAge:        ptr.Ptr(jsontime.S(2 * time.Minute)),
		Reaction:            privateAPILevel(privateAPI),
		ReactionCount:       1,
		AllowedReactions:    allTapbackEmojis(),
		ReadReceipts:        privateAPI,
		TypingNotifications: privateAPI,
	}
}

var (
	roomFeaturesPrivateAPI = makeRoomFeatures(true)
	roomFeaturesBasic      = makeRoomFeatures(false)
)

func (c *IMClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	if c.privateAPI.Load() {
		return roomFeaturesPrivateAPI
	}
	return roomFeaturesBasic
}

var _ = database.RoomTypeDM
