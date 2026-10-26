package connector

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-imessage/pkg/bluebubbles"
)

const (
	defaultBackfillCount = 50
	maxBackfillCount     = 1000
)

func (c *IMClient) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	if c.Client == nil {
		return nil, bridgev2.ErrNotLoggedIn
	}
	log := zerolog.Ctx(ctx)
	chatGUID := string(params.Portal.ID)
	query := bluebubbles.MessageQuery{
		Limit: min(max(params.Count, 1), maxBackfillCount),
		Sort:  "DESC",
	}
	if params.Count <= 0 {
		query.Limit = defaultBackfillCount
	}
	var anchorGUID string
	if params.AnchorMessage != nil {
		anchorGUID = string(params.AnchorMessage.ID)
	}
	if params.Forward && params.AnchorMessage != nil {
		query.Sort = "ASC"
		after := params.AnchorMessage.Timestamp.UnixMilli()
		query.After = &after
	} else if params.Forward {
		// A new portal has no anchor, so the initial backfill takes the newest messages.
	} else if params.Cursor != "" {
		before, err := strconv.ParseInt(string(params.Cursor), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor %q: %w", params.Cursor, err)
		}
		query.Before = &before
	} else if params.AnchorMessage != nil {
		before := params.AnchorMessage.Timestamp.UnixMilli()
		query.Before = &before
	}
	messages, err := c.Client.GetChatMessages(ctx, chatGUID, query)
	if err != nil {
		return nil, err
	}
	if query.Sort == "DESC" {
		slices.Reverse(messages)
	}
	resp := &bridgev2.FetchMessagesResponse{
		Forward: params.Forward,
		HasMore: len(messages) >= query.Limit,
	}
	byGUID := make(map[string]*bridgev2.BackfillMessage, len(messages))
	var tapbacks []*bluebubbles.Message
	var oldest int64
	for i := range messages {
		msg := &messages[i]
		if oldest == 0 || msg.DateCreated < oldest {
			oldest = msg.DateCreated
		}
		if msg.GUID == anchorGUID || msg.ItemType != bluebubbles.ItemTypeMessage || msg.DateRetracted != 0 {
			continue
		}
		if msg.IsTapback() {
			tapbacks = append(tapbacks, msg)
			continue
		}
		msg.Chats = []bluebubbles.Chat{{GUID: chatGUID}}
		sender := c.makeSender(msg)
		intent, ok := params.Portal.GetIntentFor(ctx, sender, c.UserLogin, bridgev2.RemoteEventBackfill)
		if !ok {
			continue
		}
		converted, err := c.convertMessage(ctx, params.Portal, intent, msg)
		if err != nil {
			log.Debug().Err(err).Str("message_guid", msg.GUID).Msg("Skipping message in backfill")
			continue
		}
		backfillMsg := &bridgev2.BackfillMessage{
			ConvertedMessage: converted,
			Sender:           sender,
			ID:               makeMessageID(msg.GUID),
			Timestamp:        time.UnixMilli(msg.DateCreated),
			StreamOrder:      msg.DateCreated,
		}
		byGUID[msg.GUID] = backfillMsg
		resp.Messages = append(resp.Messages, backfillMsg)
	}
	for _, tapback := range tapbacks {
		if strings.HasPrefix(tapback.AssociatedMessageType, "-") {
			continue
		}
		targetGUID, targetPart := parseAssociatedGUID(tapback.AssociatedMessageGUID)
		target, ok := byGUID[targetGUID]
		if !ok || tapbackEmoji(tapback.AssociatedMessageType) == "" {
			continue
		}
		target.Reactions = append(target.Reactions, &bridgev2.BackfillReaction{
			TargetPart: new(makePartID(targetPart)),
			Timestamp:  time.UnixMilli(tapback.DateCreated),
			Sender:     c.makeSender(tapback),
			EmojiID:    networkid.EmojiID(tapback.AssociatedMessageType),
			Emoji:      tapbackEmoji(tapback.AssociatedMessageType),
		})
	}
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		resp.MarkRead = last.IsFromMe || last.DateRead != 0
	}
	if !params.Forward && oldest != 0 {
		resp.Cursor = networkid.PaginationCursor(strconv.FormatInt(oldest, 10))
	}
	return resp, nil
}
