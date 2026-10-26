package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-imessage/pkg/bluebubbles"
)

const (
	typingTimeout     = 15 * time.Second
	ignoredEchoMaxAge = 10 * time.Minute
)

func (c *IMClient) handleSocketEvent(name string, data json.RawMessage) {
	log := c.UserLogin.Log.With().Str("action", "handle bluebubbles event").Str("event", name).Logger()
	ctx := log.WithContext(c.Main.br.BackgroundCtx)
	switch name {
	case bluebubbles.EventNewMessage, bluebubbles.EventMessageUpdated:
		var msg bluebubbles.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Err(err).Msg("Failed to parse message event")
			return
		}
		c.handleMessage(ctx, &msg, name == bluebubbles.EventMessageUpdated)
	case bluebubbles.EventMessageSendError:
		var msg bluebubbles.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Err(err).Msg("Failed to parse send error event")
			return
		}
		c.handleSendError(ctx, &msg)
	case bluebubbles.EventParticipantAdded, bluebubbles.EventParticipantRemoved, bluebubbles.EventParticipantLeft,
		bluebubbles.EventGroupNameChanged, bluebubbles.EventGroupIconChanged, bluebubbles.EventGroupIconRemoved:
		var msg bluebubbles.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Err(err).Msg("Failed to parse group event")
			return
		}
		if chatGUID := msg.ChatGUID(); chatGUID != "" {
			c.resyncChat(chatGUID)
		}
	case bluebubbles.EventChatReadStatusChanged:
		var evt bluebubbles.ChatReadStatusEvent
		if err := json.Unmarshal(data, &evt); err != nil {
			log.Err(err).Msg("Failed to parse read status event")
			return
		}
		c.handleChatReadStatus(&evt)
	case bluebubbles.EventTypingIndicator:
		var evt bluebubbles.TypingEvent
		if err := json.Unmarshal(data, &evt); err != nil {
			log.Err(err).Msg("Failed to parse typing event")
			return
		}
		c.handleTyping(&evt)
	default:
		log.Trace().RawJSON("data", data).Msg("Ignoring unknown event")
	}
}

func (c *IMClient) resyncChat(chatGUID string) {
	c.Main.br.QueueRemoteEvent(c.UserLogin, &simplevent.ChatResync{
		EventMeta: simplevent.EventMeta{
			Type:         bridgev2.RemoteEventChatResync,
			LogContext:   chatLogContext(chatGUID),
			PortalKey:    c.makePortalKey(chatGUID),
			CreatePortal: true,
		},
		GetChatInfoFunc: c.GetChatInfo,
	})
}

func (c *IMClient) handleMessage(ctx context.Context, msg *bluebubbles.Message, isUpdate bool) {
	log := zerolog.Ctx(ctx)
	chatGUID := msg.ChatGUID()
	if chatGUID == "" {
		log.Warn().Str("message_guid", msg.GUID).Msg("Ignoring message without chat")
		return
	}
	if msg.ItemType != bluebubbles.ItemTypeMessage {
		c.resyncChat(chatGUID)
		return
	}
	base := eventBase{c: c, msg: msg, chatGUID: chatGUID}
	_, isGroup, _ := bluebubbles.ParseChatGUID(chatGUID)
	if msg.IsTapback() {
		if isUpdate {
			return
		}
		if msg.IsFromMe {
			c.waitForPendingSends()
		}
		targetGUID, targetPart := parseAssociatedGUID(msg.AssociatedMessageGUID)
		name, remove := msg.AssociatedMessageType, false
		if rest, ok := strings.CutPrefix(name, "-"); ok {
			name, remove = rest, true
		}
		if tapbackEmoji(name) == "" {
			log.Debug().Str("tapback", msg.AssociatedMessageType).Msg("Ignoring unknown tapback type")
			return
		}
		c.Main.br.QueueRemoteEvent(c.UserLogin, &tapbackEvent{eventBase: base, targetGUID: targetGUID, targetPart: targetPart, name: name, remove: remove})
		return
	}
	if msg.DateRetracted != 0 {
		c.Main.br.QueueRemoteEvent(c.UserLogin, &simplevent.MessageRemove{
			EventMeta: simplevent.EventMeta{
				Type:       bridgev2.RemoteEventMessageRemove,
				LogContext: base.AddLogContext,
				PortalKey:  base.GetPortalKey(),
				Sender:     base.GetSender(),
				Timestamp:  time.UnixMilli(msg.DateRetracted),
			},
			TargetMessage: makeMessageID(msg.GUID),
		})
		return
	}
	if isUpdate {
		if msg.DateEdited != 0 {
			c.Main.br.QueueRemoteEvent(c.UserLogin, &editEvent{eventBase: base})
		}
		c.handleReceiptUpdate(ctx, msg, chatGUID, isGroup)
		return
	}
	if _, ignored := c.ignoredEchoes.Pop(msg.GUID); ignored {
		log.Debug().Str("message_guid", msg.GUID).Msg("Ignoring echo of bridge-sent message")
		return
	}
	if msg.IsFromMe {
		c.waitForPendingSends()
	}
	c.Main.br.QueueRemoteEvent(c.UserLogin, &messageEvent{eventBase: base})
	if !msg.IsFromMe && msg.DateRead != 0 {
		c.queueOwnReadReceipt(chatGUID, makeMessageID(msg.GUID), time.UnixMilli(msg.DateRead))
	}
}

// handleReceiptUpdate turns date changes on a message into receipts: the other
// side reading or receiving our message, or us reading theirs on another device.
func (c *IMClient) handleReceiptUpdate(ctx context.Context, msg *bluebubbles.Message, chatGUID string, isGroup bool) {
	if !msg.IsFromMe {
		if msg.DateRead != 0 {
			c.queueOwnReadReceipt(chatGUID, makeMessageID(msg.GUID), time.UnixMilli(msg.DateRead))
		}
		return
	}
	// Group chats don't say which member read or received the message.
	if isGroup {
		return
	}
	otherUser, err := otherUserForChat(chatGUID)
	if err != nil {
		return
	}
	meta := simplevent.EventMeta{
		LogContext: chatLogContext(chatGUID),
		PortalKey:  c.makePortalKey(chatGUID),
		Sender:     bridgev2.EventSender{Sender: otherUser},
	}
	if msg.DateRead != 0 {
		c.Main.br.QueueRemoteEvent(c.UserLogin, &simplevent.Receipt{
			EventMeta:  meta.WithType(bridgev2.RemoteEventReadReceipt).WithTimestamp(time.UnixMilli(msg.DateRead)),
			LastTarget: makeMessageID(msg.GUID),
		})
	} else if msg.DateDelivered != 0 {
		c.Main.br.QueueRemoteEvent(c.UserLogin, &simplevent.Receipt{
			EventMeta: meta.WithType(bridgev2.RemoteEventDeliveryReceipt).WithTimestamp(time.UnixMilli(msg.DateDelivered)),
			Targets:   []networkid.MessageID{makeMessageID(msg.GUID)},
		})
	}
}

func (c *IMClient) queueOwnReadReceipt(chatGUID string, target networkid.MessageID, readAt time.Time) {
	c.Main.br.QueueRemoteEvent(c.UserLogin, &simplevent.Receipt{
		EventMeta: simplevent.EventMeta{
			Type:       bridgev2.RemoteEventReadReceipt,
			LogContext: chatLogContext(chatGUID),
			PortalKey:  c.makePortalKey(chatGUID),
			Sender:     bridgev2.EventSender{IsFromMe: true, Sender: c.selfUserID()},
			Timestamp:  readAt,
		},
		LastTarget: target,
		ReadUpTo:   readAt,
	})
}

func (c *IMClient) handleChatReadStatus(evt *bluebubbles.ChatReadStatusEvent) {
	meta := simplevent.EventMeta{
		LogContext: chatLogContext(evt.ChatGUID),
		PortalKey:  c.makePortalKey(evt.ChatGUID),
		Sender:     bridgev2.EventSender{IsFromMe: true, Sender: c.selfUserID()},
	}
	if evt.Read {
		c.Main.br.QueueRemoteEvent(c.UserLogin, &simplevent.Receipt{
			EventMeta: meta.WithType(bridgev2.RemoteEventReadReceipt),
			ReadUpTo:  time.Now(),
		})
	} else {
		c.Main.br.QueueRemoteEvent(c.UserLogin, &simplevent.MarkUnread{
			EventMeta: meta.WithType(bridgev2.RemoteEventMarkUnread),
			Unread:    true,
		})
	}
}

func (c *IMClient) handleTyping(evt *bluebubbles.TypingEvent) {
	// Group typing events don't say who is typing.
	otherUser, err := otherUserForChat(evt.GUID)
	if err != nil {
		return
	}
	timeout := typingTimeout
	if !evt.Display {
		timeout = 0
	}
	c.Main.br.QueueRemoteEvent(c.UserLogin, &simplevent.Typing{
		EventMeta: simplevent.EventMeta{
			Type:       bridgev2.RemoteEventTyping,
			LogContext: chatLogContext(evt.GUID),
			PortalKey:  c.makePortalKey(evt.GUID),
			Sender:     bridgev2.EventSender{Sender: otherUser},
		},
		Timeout: timeout,
		Type:    bridgev2.TypingTypeText,
	})
}

func (c *IMClient) handleSendError(ctx context.Context, msg *bluebubbles.Message) {
	log := zerolog.Ctx(ctx).With().Str("message_guid", msg.GUID).Int("imessage_error", msg.Error).Logger()
	dbMsg, err := c.Main.br.DB.Message.GetFirstPartByID(ctx, c.UserLogin.ID, makeMessageID(msg.GUID))
	if err != nil {
		log.Err(err).Msg("Failed to look up failed message")
		return
	} else if dbMsg == nil {
		log.Debug().Msg("Failed message was not sent through the bridge")
		return
	}
	portal, err := c.Main.br.GetExistingPortalByKey(ctx, dbMsg.Room)
	if err != nil || portal == nil || portal.MXID == "" {
		log.Err(err).Msg("Failed to get portal for failed message")
		return
	}
	c.Main.br.Matrix.SendMessageStatus(ctx, &bridgev2.MessageStatus{
		Status:      event.MessageStatusFail,
		ErrorReason: event.MessageStatusNetworkError,
		Message:     fmt.Sprintf("iMessage failed to send the message (error %d)", msg.Error),
		IsCertain:   true,
		SendNotice:  true,
	}, &bridgev2.MessageStatusEventInfo{
		RoomID:        portal.MXID,
		SourceEventID: dbMsg.MXID,
		EventType:     event.EventMessage,
		Sender:        dbMsg.SenderMXID,
	})
}
