package connector

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-imessage/pkg/bluebubbles"
)

// eventBase carries what every BlueBubbles-derived remote event needs.
type eventBase struct {
	c        *IMClient
	msg      *bluebubbles.Message
	chatGUID string
}

func (e *eventBase) GetPortalKey() networkid.PortalKey {
	return e.c.makePortalKey(e.chatGUID)
}

func (e *eventBase) AddLogContext(ctx zerolog.Context) zerolog.Context {
	return ctx.
		Str("chat_guid", e.chatGUID).
		Str("message_guid", e.msg.GUID).
		Bool("is_from_me", e.msg.IsFromMe)
}

func (e *eventBase) GetSender() bridgev2.EventSender {
	return e.c.makeSender(e.msg)
}

func (e *eventBase) GetTimestamp() time.Time {
	if e.msg.DateCreated == 0 {
		return time.Now()
	}
	return time.UnixMilli(e.msg.DateCreated)
}

func (c *IMClient) makeSender(msg *bluebubbles.Message) bridgev2.EventSender {
	if msg.IsFromMe {
		return bridgev2.EventSender{IsFromMe: true, Sender: c.selfUserID()}
	}
	sender := bridgev2.EventSender{ForceDMUser: true}
	if msg.Handle != nil {
		sender.Sender = makeUserID(msg.Handle.Address)
	}
	return sender
}

type messageEvent struct {
	eventBase
}

var (
	_ bridgev2.RemoteMessage                  = (*messageEvent)(nil)
	_ bridgev2.RemoteMessageWithTransactionID = (*messageEvent)(nil)
	_ bridgev2.RemoteEventThatMayCreatePortal = (*messageEvent)(nil)
	_ bridgev2.RemoteEventWithTimestamp       = (*messageEvent)(nil)
)

func (e *messageEvent) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventMessage
}

func (e *messageEvent) GetID() networkid.MessageID {
	return makeMessageID(e.msg.GUID)
}

func (e *messageEvent) GetTransactionID() networkid.TransactionID {
	return networkid.TransactionID(e.msg.TempGUID)
}

func (e *messageEvent) ShouldCreatePortal() bool {
	return true
}

func (e *messageEvent) ConvertMessage(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) (*bridgev2.ConvertedMessage, error) {
	return e.c.convertMessage(ctx, portal, intent, e.msg)
}

type tapbackEvent struct {
	eventBase
	targetGUID string
	targetPart int
	name       string
	remove     bool
}

var (
	_ bridgev2.RemoteReaction            = (*tapbackEvent)(nil)
	_ bridgev2.RemoteReactionRemove      = (*tapbackEvent)(nil)
	_ bridgev2.RemoteEventWithTargetPart = (*tapbackEvent)(nil)
	_ bridgev2.RemoteEventWithTimestamp  = (*tapbackEvent)(nil)
)

func (e *tapbackEvent) GetType() bridgev2.RemoteEventType {
	if e.remove {
		return bridgev2.RemoteEventReactionRemove
	}
	return bridgev2.RemoteEventReaction
}

func (e *tapbackEvent) AddLogContext(ctx zerolog.Context) zerolog.Context {
	return e.eventBase.AddLogContext(ctx).
		Str("target_guid", e.targetGUID).
		Int("target_part", e.targetPart).
		Str("tapback", e.name).
		Bool("remove", e.remove)
}

func (e *tapbackEvent) GetTargetMessage() networkid.MessageID {
	return makeMessageID(e.targetGUID)
}

func (e *tapbackEvent) GetTargetMessagePart() networkid.PartID {
	return makePartID(e.targetPart)
}

func (e *tapbackEvent) GetReactionEmoji() (string, networkid.EmojiID) {
	return tapbackEmoji(e.name), networkid.EmojiID(e.name)
}

func (e *tapbackEvent) GetRemovedEmojiID() networkid.EmojiID {
	return networkid.EmojiID(e.name)
}

type editEvent struct {
	eventBase
}

var (
	_ bridgev2.RemoteEdit               = (*editEvent)(nil)
	_ bridgev2.RemoteEventWithTimestamp = (*editEvent)(nil)
)

func (e *editEvent) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventEdit
}

func (e *editEvent) GetTargetMessage() networkid.MessageID {
	return makeMessageID(e.msg.GUID)
}

func (e *editEvent) GetTimestamp() time.Time {
	if e.msg.DateEdited == 0 {
		return time.Now()
	}
	return time.UnixMilli(e.msg.DateEdited)
}

func (e *editEvent) ConvertEdit(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, existing []*database.Message) (*bridgev2.ConvertedEdit, error) {
	return e.c.convertEdit(ctx, e.msg, existing)
}
