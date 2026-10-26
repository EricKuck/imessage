package bluebubbles

import "encoding/json"

// Websocket event names, see packages/server/src/server/events.ts in the BlueBubbles server.
const (
	EventNewMessage             = "new-message"
	EventMessageUpdated         = "updated-message"
	EventMessageSendError       = "message-send-error"
	EventParticipantAdded       = "participant-added"
	EventParticipantRemoved     = "participant-removed"
	EventParticipantLeft        = "participant-left"
	EventGroupNameChanged       = "group-name-change"
	EventGroupIconChanged       = "group-icon-changed"
	EventGroupIconRemoved       = "group-icon-removed"
	EventChatReadStatusChanged  = "chat-read-status-changed"
	EventTypingIndicator        = "typing-indicator"
	EventIMessageAliasesRemoved = "imessage-aliases-removed"
)

const (
	MethodPrivateAPI  = "private-api"
	MethodAppleScript = "apple-script"

	ServiceIMessage = "iMessage"
	ServiceSMS      = "SMS"

	ChatStyleGroup = 43
	ChatStyleDM    = 45
)

// Message item types and group action subtypes from the iMessage database.
const (
	ItemTypeMessage = 0
	ItemTypeMember  = 1
	ItemTypeName    = 2
	ItemTypeAvatar  = 3
)

type ServerInfo struct {
	OSVersion        string `json:"os_version"`
	ServerVersion    string `json:"server_version"`
	PrivateAPI       bool   `json:"private_api"`
	HelperConnected  bool   `json:"helper_connected"`
	DetectedICloud   string `json:"detected_icloud"`
	DetectedIMessage string `json:"detected_imessage"`
}

type Chat struct {
	GUID           string           `json:"guid"`
	Style          int              `json:"style"`
	ChatIdentifier string           `json:"chatIdentifier"`
	DisplayName    string           `json:"displayName"`
	IsArchived     bool             `json:"isArchived"`
	GroupID        string           `json:"groupId"`
	Participants   []Handle         `json:"participants"`
	LastMessage    *Message         `json:"lastMessage"`
	Properties     []ChatProperties `json:"properties"`
	// Only present in chat creation responses.
	Messages []Message `json:"messages"`
}

func (c *Chat) IsGroup() bool {
	if c.Style != 0 {
		return c.Style == ChatStyleGroup
	}
	_, isGroup, _ := ParseChatGUID(c.GUID)
	return isGroup
}

// GroupPhotoGUID returns the attachment GUID of the group icon, or "" if there is none.
func (c *Chat) GroupPhotoGUID() string {
	for _, prop := range c.Properties {
		if prop.GroupPhotoGUID != nil {
			return *prop.GroupPhotoGUID
		}
	}
	return ""
}

type ChatProperties struct {
	GroupPhotoGUID *string `json:"groupPhotoGuid"`
}

type Handle struct {
	Address string `json:"address"`
	Service string `json:"service"`
	Country string `json:"country"`
}

type Message struct {
	GUID                  string       `json:"guid"`
	TempGUID              string       `json:"tempGuid"`
	Text                  string       `json:"text"`
	Subject               string       `json:"subject"`
	Handle                *Handle      `json:"handle"`
	Chats                 []Chat       `json:"chats"`
	Attachments           []Attachment `json:"attachments"`
	Error                 int          `json:"error"`
	DateCreated           int64        `json:"dateCreated"`
	DateRead              int64        `json:"dateRead"`
	DateDelivered         int64        `json:"dateDelivered"`
	DateEdited            int64        `json:"dateEdited"`
	DateRetracted         int64        `json:"dateRetracted"`
	IsFromMe              bool         `json:"isFromMe"`
	IsAudioMessage        bool         `json:"isAudioMessage"`
	ItemType              int          `json:"itemType"`
	GroupTitle            string       `json:"groupTitle"`
	GroupActionType       int          `json:"groupActionType"`
	BalloonBundleID       string       `json:"balloonBundleId"`
	AssociatedMessageGUID string       `json:"associatedMessageGuid"`
	AssociatedMessageType string       `json:"associatedMessageType"`
	ThreadOriginatorGUID  string       `json:"threadOriginatorGuid"`
	ThreadOriginatorPart  string       `json:"threadOriginatorPart"`
	PartCount             int          `json:"partCount"`
}

func (m *Message) ChatGUID() string {
	if len(m.Chats) == 0 {
		return ""
	}
	return m.Chats[0].GUID
}

// IsTapback reports whether the message is a reaction rather than a normal message.
// Stickers also use the associated message fields, but they're bridged as messages.
func (m *Message) IsTapback() bool {
	return m.AssociatedMessageGUID != "" && m.AssociatedMessageType != "" && m.AssociatedMessageType != "sticker"
}

func (m *Message) IsSticker() bool {
	return m.AssociatedMessageGUID != "" && m.AssociatedMessageType == "sticker"
}

type Attachment struct {
	GUID           string `json:"guid"`
	UTI            string `json:"uti"`
	MimeType       string `json:"mimeType"`
	TransferName   string `json:"transferName"`
	TotalBytes     int64  `json:"totalBytes"`
	TransferState  int    `json:"transferState"`
	IsOutgoing     bool   `json:"isOutgoing"`
	HideAttachment bool   `json:"hideAttachment"`
	IsSticker      bool   `json:"isSticker"`
	OriginalGUID   string `json:"originalGuid"`
	HasLivePhoto   bool   `json:"hasLivePhoto"`
	Height         int64  `json:"height"`
	Width          int64  `json:"width"`
}

type Contact struct {
	ID           any              `json:"id"`
	FirstName    string           `json:"firstName"`
	LastName     string           `json:"lastName"`
	DisplayName  string           `json:"displayName"`
	Nickname     string           `json:"nickname"`
	PhoneNumbers []ContactAddress `json:"phoneNumbers"`
	Emails       []ContactAddress `json:"emails"`
	// Base64-encoded image, only present when requested with extraProperties=avatar.
	Avatar     string `json:"avatar"`
	SourceType string `json:"sourceType"`
}

type ContactAddress struct {
	Address string `json:"address"`
}

type TypingEvent struct {
	Display bool   `json:"display"`
	GUID    string `json:"guid"`
}

type ChatReadStatusEvent struct {
	ChatGUID string `json:"chatGuid"`
	Read     bool   `json:"read"`
}

type SendTextRequest struct {
	ChatGUID            string `json:"chatGuid"`
	TempGUID            string `json:"tempGuid"`
	Method              string `json:"method"`
	Message             string `json:"message"`
	Subject             string `json:"subject,omitempty"`
	SelectedMessageGUID string `json:"selectedMessageGuid,omitempty"`
	PartIndex           int    `json:"partIndex"`
}

type SendAttachmentRequest struct {
	ChatGUID            string
	TempGUID            string
	Method              string
	Name                string
	SelectedMessageGUID string
	PartIndex           int
	IsAudioMessage      bool
}

type SendReactionRequest struct {
	ChatGUID            string `json:"chatGuid"`
	SelectedMessageGUID string `json:"selectedMessageGuid"`
	Reaction            string `json:"reaction"`
	PartIndex           int    `json:"partIndex"`
}

type EditMessageRequest struct {
	EditedMessage                 string `json:"editedMessage"`
	BackwardsCompatibilityMessage string `json:"backwardsCompatibilityMessage"`
	PartIndex                     int    `json:"partIndex"`
}

type UnsendMessageRequest struct {
	PartIndex int `json:"partIndex"`
}

type CreateChatRequest struct {
	Addresses []string `json:"addresses"`
	Message   string   `json:"message"`
	Method    string   `json:"method"`
	Service   string   `json:"service"`
	TempGUID  string   `json:"tempGuid,omitempty"`
}

type ChatQueryRequest struct {
	Limit  int      `json:"limit"`
	Offset int      `json:"offset"`
	With   []string `json:"with"`
	Sort   string   `json:"sort"`
}

type MessageQuery struct {
	Limit  int
	Offset int
	// "ASC" or "DESC"
	Sort string
	// Unix millisecond bounds on dateCreated.
	Before *int64
	After  *int64
}

type PageMetadata struct {
	Count  int64 `json:"count"`
	Total  int64 `json:"total"`
	Offset int64 `json:"offset"`
	Limit  int64 `json:"limit"`
}

type response struct {
	Status   int             `json:"status"`
	Message  string          `json:"message"`
	Error    json.RawMessage `json:"error"`
	Data     json.RawMessage `json:"data"`
	Metadata *PageMetadata   `json:"metadata"`
}

type responseError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
