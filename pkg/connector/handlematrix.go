package connector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ffmpeg"
	"go.mau.fi/util/random"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-imessage/pkg/bluebubbles"
)

var (
	ErrPrivateAPIRequired = bridgev2.WrapErrorInStatus(errors.New("the BlueBubbles private API is required for this")).
				WithIsCertain(true).WithErrorAsMessage().WithErrorReason(event.MessageStatusUnsupported)
	ErrUnsupportedTapback = bridgev2.WrapErrorInStatus(errors.New("iMessage only supports the love, like, dislike, laugh, emphasize and question tapbacks")).
				WithIsCertain(true).WithErrorAsMessage().WithErrorReason(event.MessageStatusUnsupported)
	ErrNewChatNeedsText = bridgev2.WrapErrorInStatus(errors.New("send a text message first to start the chat")).
				WithIsCertain(true).WithErrorAsMessage().WithErrorReason(event.MessageStatusUnsupported)
	ErrMessageFailed = bridgev2.WrapErrorInStatus(errors.New("iMessage failed to send the message")).
				WithIsCertain(true).WithSendNotice(true).WithErrorReason(event.MessageStatusNetworkError)
)

func wrapSendError(err error) error {
	var apiErr *bluebubbles.APIError
	if errors.As(err, &apiErr) {
		return bridgev2.WrapErrorInStatus(err).WithMessage(apiErr.Error()).WithIsCertain(true).WithSendNotice(true).WithErrorReason(event.MessageStatusNetworkError)
	}
	return bridgev2.WrapErrorInStatus(err).WithIsCertain(false).WithSendNotice(true).WithErrorReason(event.MessageStatusNetworkError)
}

func tempGUID() string {
	return "temp-" + random.String(16)
}

func (c *IMClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if c.Client == nil {
		return nil, bridgev2.ErrNotLoggedIn
	}
	chatGUID := string(msg.Portal.ID)
	var replyTo string
	var replyPart int
	if msg.ReplyTo != nil {
		replyTo = string(msg.ReplyTo.ID)
		replyPart = parsePartID(msg.ReplyTo.PartID)
	}
	c.pendingSends.Add(1)
	defer c.pendingSends.Add(-1)

	var sent *bluebubbles.Message
	var err error
	var textHash string
	switch msg.Content.MsgType {
	case event.MsgText, event.MsgEmote, event.MsgNotice:
		text := msg.Content.Body
		if msg.Content.MsgType == event.MsgEmote {
			text = "/me " + text
		}
		textHash = hashText(text)
		sent, err = c.sendText(ctx, msg.Portal, chatGUID, text, replyTo, replyPart)
	case event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		sent, err = c.sendMedia(ctx, msg, chatGUID, replyTo, replyPart)
	default:
		return nil, fmt.Errorf("%w %s", bridgev2.ErrUnsupportedMessageType, msg.Content.MsgType)
	}
	if err != nil {
		return nil, err
	}
	if sent.Error != 0 {
		return nil, fmt.Errorf("%w (error %d)", ErrMessageFailed, sent.Error)
	}
	return &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			ID:        makeMessageID(sent.GUID),
			PartID:    makePartID(0),
			SenderID:  c.selfUserID(),
			Timestamp: sentTimestamp(sent),
			Metadata:  &MessageMetadata{TextHash: textHash},
		},
	}, nil
}

func sentTimestamp(msg *bluebubbles.Message) time.Time {
	if msg.DateCreated == 0 {
		return time.Now()
	}
	return time.UnixMilli(msg.DateCreated)
}

func (c *IMClient) sendText(ctx context.Context, portal *bridgev2.Portal, chatGUID, text, replyTo string, replyPart int) (*bluebubbles.Message, error) {
	if meta := portal.Metadata.(*PortalMetadata); meta.NeedsCreate {
		return c.createChatWithMessage(ctx, portal, chatGUID, text)
	}
	sent, err := c.Client.SendText(ctx, &bluebubbles.SendTextRequest{
		ChatGUID:            chatGUID,
		TempGUID:            tempGUID(),
		Method:              c.sendMethod(),
		Message:             text,
		SelectedMessageGUID: replyTo,
		PartIndex:           replyPart,
	})
	if err != nil {
		return nil, wrapSendError(err)
	}
	return sent, nil
}

// createChatWithMessage sends the first message of a DM that was started from Matrix.
func (c *IMClient) createChatWithMessage(ctx context.Context, portal *bridgev2.Portal, chatGUID, text string) (*bluebubbles.Message, error) {
	service, _, address := bluebubbles.ParseChatGUID(chatGUID)
	chat, err := c.Client.CreateChat(ctx, &bluebubbles.CreateChatRequest{
		Addresses: []string{address},
		Message:   text,
		Method:    c.sendMethod(),
		Service:   service,
		TempGUID:  tempGUID(),
	})
	if err != nil {
		return nil, wrapSendError(err)
	}
	meta := portal.Metadata.(*PortalMetadata)
	meta.NeedsCreate = false
	if err = portal.Save(ctx); err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to save portal after creating chat")
	}
	if chat.GUID != chatGUID {
		zerolog.Ctx(ctx).Warn().Str("created_chat_guid", chat.GUID).Msg("Created chat has a different GUID than the portal")
	}
	if len(chat.Messages) == 0 {
		return nil, fmt.Errorf("%w: chat creation response has no message", ErrMessageFailed)
	}
	return &chat.Messages[0], nil
}

func (c *IMClient) sendMedia(ctx context.Context, msg *bridgev2.MatrixMessage, chatGUID, replyTo string, replyPart int) (*bluebubbles.Message, error) {
	if msg.Portal.Metadata.(*PortalMetadata).NeedsCreate {
		return nil, ErrNewChatNeedsText
	}
	content := msg.Content
	data, err := c.Main.br.Bot.DownloadMedia(ctx, content.URL, content.File)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", bridgev2.ErrMediaDownloadFailed, err)
	}
	mimeType := ""
	if content.Info != nil {
		mimeType = content.Info.MimeType
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	fileName := content.Body
	if content.FileName != "" {
		fileName = content.FileName
	}
	isVoice := content.MsgType == event.MsgAudio && content.MSC3245Voice != nil
	if isVoice && mimeType != "audio/mp4" {
		if !ffmpeg.Supported() {
			return nil, fmt.Errorf("%w: ffmpeg is required to send voice messages", bridgev2.ErrMediaConvertFailed)
		}
		data, err = ffmpeg.ConvertBytes(ctx, data, ".m4a", nil, []string{"-c:a", "aac"}, mimeType)
		if err != nil {
			return nil, fmt.Errorf("%w (%s to m4a): %w", bridgev2.ErrMediaConvertFailed, mimeType, err)
		}
		fileName = fileNameForMime(fileName, "audio/mp4")
	}
	sent, err := c.Client.SendAttachment(ctx, &bluebubbles.SendAttachmentRequest{
		ChatGUID:            chatGUID,
		TempGUID:            tempGUID(),
		Method:              c.sendMethod(),
		Name:                fileName,
		SelectedMessageGUID: replyTo,
		PartIndex:           replyPart,
		IsAudioMessage:      isVoice,
	}, data)
	if err != nil {
		return nil, wrapSendError(err)
	}
	// iMessage has no captions, so the caption goes out as a second message
	// whose echo must not come back as a separate Matrix message.
	if content.FileName != "" && content.Body != "" && content.Body != content.FileName {
		caption, err := c.Client.SendText(ctx, &bluebubbles.SendTextRequest{
			ChatGUID: chatGUID,
			TempGUID: tempGUID(),
			Method:   c.sendMethod(),
			Message:  content.Body,
		})
		if err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to send caption as a separate message")
		} else if caption.GUID != "" {
			c.ignoredEchoes.Set(caption.GUID, time.Now())
			c.pruneIgnoredEchoes()
		}
	}
	return sent, nil
}

func (c *IMClient) pruneIgnoredEchoes() {
	for guid, added := range c.ignoredEchoes.Iter() {
		if time.Since(added) > ignoredEchoMaxAge {
			c.ignoredEchoes.Delete(guid)
		}
	}
}

func (c *IMClient) HandleMatrixEdit(ctx context.Context, msg *bridgev2.MatrixEdit) error {
	if c.Client == nil {
		return bridgev2.ErrNotLoggedIn
	} else if !c.privateAPI.Load() {
		return ErrPrivateAPIRequired
	}
	text := msg.Content.Body
	_, err := c.Client.EditMessage(ctx, string(msg.EditTarget.ID), &bluebubbles.EditMessageRequest{
		EditedMessage:                 text,
		BackwardsCompatibilityMessage: fmt.Sprintf("Edited to \"%s\"", text),
		PartIndex:                     parsePartID(msg.EditTarget.PartID),
	})
	if err != nil {
		return wrapSendError(err)
	}
	// Remember the new text so the edit's echo isn't bridged back.
	meta, ok := msg.EditTarget.Metadata.(*MessageMetadata)
	if !ok {
		meta = &MessageMetadata{}
		msg.EditTarget.Metadata = meta
	}
	meta.TextHash = hashText(text)
	return nil
}

func (c *IMClient) HandleMatrixMessageRemove(ctx context.Context, msg *bridgev2.MatrixMessageRemove) error {
	if c.Client == nil {
		return bridgev2.ErrNotLoggedIn
	} else if !c.privateAPI.Load() {
		return ErrPrivateAPIRequired
	}
	_, err := c.Client.UnsendMessage(ctx, string(msg.TargetMessage.ID), parsePartID(msg.TargetMessage.PartID))
	if err != nil {
		return wrapSendError(err)
	}
	return nil
}

func (c *IMClient) PreHandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	if !c.privateAPI.Load() {
		return bridgev2.MatrixReactionPreResponse{}, ErrPrivateAPIRequired
	}
	name := tapbackFromEmoji(msg.Content.RelatesTo.Key)
	if name == "" {
		return bridgev2.MatrixReactionPreResponse{}, ErrUnsupportedTapback
	}
	return bridgev2.MatrixReactionPreResponse{
		SenderID:     c.selfUserID(),
		EmojiID:      networkid.EmojiID(name),
		Emoji:        tapbackEmoji(name),
		MaxReactions: 1,
	}, nil
}

func (c *IMClient) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (*database.Reaction, error) {
	if c.Client == nil {
		return nil, bridgev2.ErrNotLoggedIn
	}
	c.pendingSends.Add(1)
	defer c.pendingSends.Add(-1)
	_, err := c.Client.SendReaction(ctx, &bluebubbles.SendReactionRequest{
		ChatGUID:            string(msg.Portal.ID),
		SelectedMessageGUID: string(msg.TargetMessage.ID),
		Reaction:            string(msg.PreHandleResp.EmojiID),
		PartIndex:           parsePartID(msg.TargetMessage.PartID),
	})
	if err != nil {
		return nil, wrapSendError(err)
	}
	return &database.Reaction{}, nil
}

func (c *IMClient) HandleMatrixReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) error {
	if c.Client == nil {
		return bridgev2.ErrNotLoggedIn
	} else if !c.privateAPI.Load() {
		return ErrPrivateAPIRequired
	}
	c.pendingSends.Add(1)
	defer c.pendingSends.Add(-1)
	_, err := c.Client.SendReaction(ctx, &bluebubbles.SendReactionRequest{
		ChatGUID:            string(msg.Portal.ID),
		SelectedMessageGUID: string(msg.TargetReaction.MessageID),
		Reaction:            "-" + string(msg.TargetReaction.EmojiID),
		PartIndex:           parsePartID(msg.TargetReaction.MessagePartID),
	})
	if err != nil {
		return wrapSendError(err)
	}
	return nil
}

func (c *IMClient) HandleMatrixReadReceipt(ctx context.Context, msg *bridgev2.MatrixReadReceipt) error {
	if c.Client == nil || !c.privateAPI.Load() {
		return nil
	}
	return c.Client.MarkChatRead(ctx, string(msg.Portal.ID))
}

func (c *IMClient) HandleMatrixTyping(ctx context.Context, msg *bridgev2.MatrixTyping) error {
	if c.Client == nil || !c.privateAPI.Load() {
		return nil
	}
	return c.Client.SetTyping(ctx, string(msg.Portal.ID), msg.IsTyping)
}
