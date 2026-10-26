package connector

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"

	"go.mau.fi/mautrix-imessage/pkg/bluebubbles"
)

var ErrInvalidIdentifier = bridgev2.WrapRespErrManual(errors.New("identifier must be a phone number or email address"), mautrix.MInvalidParam.ErrCode, http.StatusBadRequest)

func cleanIdentifier(identifier string) (string, error) {
	identifier = normalizeAddress(identifier)
	if isEmail(identifier) {
		return identifier, nil
	}
	digits := digitsOnly(identifier)
	if len(digits) < 3 {
		return "", ErrInvalidIdentifier
	}
	return "+" + digits, nil
}

func (c *IMClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	if c.Client == nil {
		return nil, bridgev2.ErrNotLoggedIn
	}
	address, err := cleanIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	service := bluebubbles.ServiceIMessage
	handle, err := c.Client.GetHandle(ctx, address)
	if err == nil && handle.Address != "" {
		address = normalizeAddress(handle.Address)
		if handle.Service != "" {
			service = handle.Service
		}
	} else if err != nil {
		zerolog.Ctx(ctx).Debug().Err(err).Str("address", address).Msg("Handle not found on the Mac, assuming iMessage")
	}
	userID := makeUserID(address)
	ghost, err := c.Main.br.GetGhostByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get ghost: %w", err)
	}
	resp := &bridgev2.ResolveIdentifierResponse{
		Ghost:    ghost,
		UserID:   userID,
		UserInfo: c.userInfoForAddress(ctx, address),
	}
	if !createChat {
		return resp, nil
	}
	chatGUID := bluebubbles.MakeDMChatGUID(service, address)
	chat, err := c.Client.GetChat(ctx, chatGUID)
	var apiErr *bluebubbles.APIError
	needsCreate := false
	if errors.As(err, &apiErr) && apiErr.IsNotFound() {
		chat = &bluebubbles.Chat{GUID: chatGUID}
		needsCreate = true
	} else if err != nil {
		return nil, err
	}
	info := c.wrapChatInfo(ctx, chat)
	if needsCreate {
		prev := info.ExtraUpdates
		info.ExtraUpdates = func(ctx context.Context, portal *bridgev2.Portal) bool {
			changed := prev(ctx, portal)
			meta := portal.Metadata.(*PortalMetadata)
			if !meta.NeedsCreate {
				meta.NeedsCreate = true
				changed = true
			}
			return changed
		}
	}
	resp.Chat = &bridgev2.CreateChatResponse{
		PortalKey:  c.makePortalKey(chatGUID),
		PortalInfo: info,
	}
	return resp, nil
}
