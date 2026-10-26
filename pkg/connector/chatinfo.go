package connector

import (
	"context"
	"errors"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-imessage/pkg/bluebubbles"
)

func (c *IMClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	chatGUID := string(portal.ID)
	chat, err := c.Client.GetChat(ctx, chatGUID)
	var apiErr *bluebubbles.APIError
	if errors.As(err, &apiErr) && apiErr.IsNotFound() {
		// DMs started from Matrix don't exist on the Mac until the first message is sent.
		if _, isGroup, _ := bluebubbles.ParseChatGUID(chatGUID); !isGroup {
			return c.wrapChatInfo(ctx, &bluebubbles.Chat{GUID: chatGUID}), nil
		}
		return nil, err
	} else if err != nil {
		return nil, err
	}
	return c.wrapChatInfo(ctx, chat), nil
}

func (c *IMClient) wrapChatInfo(ctx context.Context, chat *bluebubbles.Chat) *bridgev2.ChatInfo {
	service, isGroup, identifier := bluebubbles.ParseChatGUID(chat.GUID)
	existsOnMac := chat.Style != 0 || len(chat.Participants) > 0
	members := &bridgev2.ChatMemberList{
		IsFull: true,
		MemberMap: bridgev2.ChatMemberMap{
			"": {EventSender: bridgev2.EventSender{IsFromMe: true}},
		},
	}
	addresses := make([]string, 0, len(chat.Participants))
	for _, participant := range chat.Participants {
		addresses = append(addresses, participant.Address)
	}
	if len(addresses) == 0 && !isGroup && identifier != "" {
		addresses = append(addresses, identifier)
	}
	for _, address := range addresses {
		userID := makeUserID(address)
		members.MemberMap[userID] = bridgev2.ChatMember{
			EventSender: bridgev2.EventSender{Sender: userID},
			UserInfo:    c.userInfoForAddress(ctx, address),
		}
	}
	info := &bridgev2.ChatInfo{
		Members:     members,
		CanBackfill: true,
		ExtraUpdates: func(ctx context.Context, portal *bridgev2.Portal) (changed bool) {
			meta := portal.Metadata.(*PortalMetadata)
			if meta.Service != service {
				meta.Service = service
				changed = true
			}
			if meta.NeedsCreate && existsOnMac {
				meta.NeedsCreate = false
				changed = true
			}
			return changed
		},
	}
	if isGroup {
		info.Type = new(database.RoomTypeDefault)
		info.Name = new(chat.DisplayName)
		if photoGUID := chat.GroupPhotoGUID(); photoGUID != "" {
			info.Avatar = &bridgev2.Avatar{
				ID: networkid.AvatarID(photoGUID),
				Get: func(ctx context.Context) ([]byte, error) {
					data, _, err := c.Client.DownloadChatIcon(ctx, chat.GUID)
					return data, err
				},
			}
		} else {
			info.Avatar = &bridgev2.Avatar{Remove: true}
		}
	} else {
		info.Type = new(database.RoomTypeDM)
		info.Name = bridgev2.DefaultChatName
		if len(addresses) == 1 {
			members.OtherUserID = makeUserID(addresses[0])
		}
	}
	return info
}

// otherUserForChat returns the ghost ID of the other side of a DM chat GUID.
func otherUserForChat(chatGUID string) (networkid.UserID, error) {
	_, isGroup, identifier := bluebubbles.ParseChatGUID(chatGUID)
	if isGroup || identifier == "" {
		return "", fmt.Errorf("chat %s is not a DM", chatGUID)
	}
	return makeUserID(identifier), nil
}
