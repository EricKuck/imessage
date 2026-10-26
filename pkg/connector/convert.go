package connector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/rs/zerolog"
	"go.mau.fi/util/exmime"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-imessage/pkg/bluebubbles"
)

// Attachments are parts 0..n-1 and the text is part n, matching the part
// indexes iMessage uses for reply and tapback targets.
func (c *IMClient) convertMessage(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg *bluebubbles.Message) (*bridgev2.ConvertedMessage, error) {
	log := zerolog.Ctx(ctx)
	cm := &bridgev2.ConvertedMessage{}
	if msg.ThreadOriginatorGUID != "" {
		cm.ReplyTo = &networkid.MessageOptionalPartID{
			MessageID: makeMessageID(msg.ThreadOriginatorGUID),
			PartID:    new(makePartID(parseThreadOriginatorPart(msg.ThreadOriginatorPart))),
		}
	} else if msg.IsSticker() {
		targetGUID, targetPart := parseAssociatedGUID(msg.AssociatedMessageGUID)
		cm.ReplyTo = &networkid.MessageOptionalPartID{
			MessageID: makeMessageID(targetGUID),
			PartID:    new(makePartID(targetPart)),
		}
	}
	for i, attachment := range msg.Attachments {
		part, err := c.convertAttachment(ctx, portal, intent, msg, &attachment)
		if err != nil {
			log.Err(err).Str("attachment_guid", attachment.GUID).Msg("Failed to bridge attachment")
			part = &bridgev2.ConvertedMessagePart{
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType: event.MsgNotice,
					Body:    fmt.Sprintf("Failed to bridge attachment %s", attachment.TransferName),
				},
			}
		}
		part.ID = makePartID(i)
		cm.Parts = append(cm.Parts, part)
	}
	if text := convertText(msg); text != nil {
		text.ID = makePartID(len(msg.Attachments))
		cm.Parts = append(cm.Parts, text)
	}
	if len(cm.Parts) == 0 {
		if msg.BalloonBundleID == "" {
			return nil, fmt.Errorf("%w: message has no content", bridgev2.ErrIgnoringRemoteEvent)
		}
		cm.Parts = append(cm.Parts, &bridgev2.ConvertedMessagePart{
			ID:   makePartID(0),
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType: event.MsgNotice,
				Body:    fmt.Sprintf("Unsupported iMessage app message (%s)", path.Base(msg.BalloonBundleID)),
			},
		})
	}
	return cm, nil
}

func messageText(msg *bluebubbles.Message) (text, subject string) {
	// U+FFFC marks where attachments sit in the text.
	text = strings.ReplaceAll(msg.Text, "￼", "")
	subject = strings.ReplaceAll(msg.Subject, "￼", "")
	return strings.TrimSpace(text), strings.TrimSpace(subject)
}

func convertText(msg *bluebubbles.Message) *bridgev2.ConvertedMessagePart {
	text, subject := messageText(msg)
	if text == "" && subject == "" {
		return nil
	}
	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    text,
	}
	if subject != "" {
		content.Format = event.FormatHTML
		content.FormattedBody = fmt.Sprintf("<strong>%s</strong><br>%s", event.TextToHTML(subject), event.TextToHTML(text))
		content.Body = fmt.Sprintf("**%s**\n%s", subject, text)
	}
	return &bridgev2.ConvertedMessagePart{
		Type:       event.EventMessage,
		Content:    content,
		DBMetadata: &MessageMetadata{TextHash: hashText(content.Body)},
	}
}

func (c *IMClient) convertAttachment(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg *bluebubbles.Message, attachment *bluebubbles.Attachment) (*bridgev2.ConvertedMessagePart, error) {
	resp, err := c.Client.DownloadAttachment(ctx, attachment.GUID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", bridgev2.ErrMediaDownloadFailed, err)
	}
	defer resp.Body.Close()
	mimeType := resp.Header.Get("Content-Type")
	if parsed, _, err := mime.ParseMediaType(mimeType); err == nil {
		mimeType = parsed
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = attachment.MimeType
	}
	fileName := fileNameForMime(attachment.TransferName, mimeType)
	content := &event.MessageEventContent{
		Body: fileName,
		Info: &event.FileInfo{
			MimeType: mimeType,
			Width:    int(attachment.Width),
			Height:   int(attachment.Height),
		},
	}
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		content.MsgType = event.MsgImage
	case strings.HasPrefix(mimeType, "video/"):
		content.MsgType = event.MsgVideo
	case strings.HasPrefix(mimeType, "audio/"):
		content.MsgType = event.MsgAudio
		if msg.IsAudioMessage {
			content.MSC3245Voice = &event.MSC3245Voice{}
		}
	default:
		content.MsgType = event.MsgFile
	}
	size := resp.ContentLength
	if size <= 0 {
		size = attachment.TotalBytes
	}
	detectMime := mimeType == ""
	content.URL, content.File, err = intent.UploadMediaStream(ctx, portal.MXID, size, detectMime, func(file io.Writer) (*bridgev2.FileStreamResult, error) {
		n, err := io.Copy(file, resp.Body)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", bridgev2.ErrMediaDownloadFailed, err)
		}
		content.Info.Size = int(n)
		if detectMime {
			header := make([]byte, 512)
			read, err := file.(*os.File).ReadAt(header, 0)
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("failed to read file header: %w", err)
			}
			mimeType = http.DetectContentType(header[:read])
			content.Info.MimeType = mimeType
			fileName = fileNameForMime(fileName, mimeType)
			content.Body = fileName
		}
		return &bridgev2.FileStreamResult{FileName: fileName, MimeType: mimeType}, nil
	})
	if err != nil {
		if errors.Is(err, bridgev2.ErrMediaDownloadFailed) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", bridgev2.ErrMediaReuploadFailed, err)
	}
	part := &bridgev2.ConvertedMessagePart{
		Type:       event.EventMessage,
		Content:    content,
		DBMetadata: &MessageMetadata{AttachmentGUID: attachment.GUID},
	}
	if attachment.IsSticker || msg.IsSticker() {
		part.Type = event.EventSticker
	}
	return part, nil
}

// fileNameForMime swaps the extension when BlueBubbles converted the file
// (HEIC to JPEG, CAF to MP3) so clients don't see a mismatched name.
func fileNameForMime(name, mimeType string) string {
	if name == "" {
		name = "attachment"
	}
	if mimeType == "" {
		return name
	}
	ext := path.Ext(name)
	if ext != "" {
		if current := mime.TypeByExtension(ext); current != "" && strings.EqualFold(strings.Split(current, ";")[0], mimeType) {
			return name
		}
	}
	newExt := exmime.ExtensionFromMimetype(mimeType)
	if newExt == "" {
		return name
	}
	return strings.TrimSuffix(name, ext) + newExt
}

func (c *IMClient) convertEdit(ctx context.Context, msg *bluebubbles.Message, existing []*database.Message) (*bridgev2.ConvertedEdit, error) {
	text := convertText(msg)
	if text == nil {
		return nil, fmt.Errorf("%w: edited message has no text", bridgev2.ErrIgnoringRemoteEvent)
	}
	// The text is always the last part.
	var target *database.Message
	for _, part := range existing {
		if target == nil || parsePartID(part.PartID) > parsePartID(target.PartID) {
			target = part
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w: no existing parts", bridgev2.ErrIgnoringRemoteEvent)
	}
	newHash := text.DBMetadata.(*MessageMetadata).TextHash
	if meta, ok := target.Metadata.(*MessageMetadata); ok && meta.TextHash == newHash {
		return nil, fmt.Errorf("%w: text didn't change", bridgev2.ErrIgnoringRemoteEvent)
	}
	return &bridgev2.ConvertedEdit{ModifiedParts: []*bridgev2.ConvertedEditPart{text.ToEditPart(target)}}, nil
}
