package connector

import (
	"maunium.net/go/mautrix/bridgev2/database"
)

func (ic *IMConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Portal:    func() any { return &PortalMetadata{} },
		Message:   func() any { return &MessageMetadata{} },
		UserLogin: func() any { return &UserLoginMetadata{} },
	}
}

type UserLoginMetadata struct {
	URL      string `json:"url"`
	Password string `json:"password"`
	// Addresses the Mac is signed in with, used to recognize our own handles.
	Accounts []string `json:"accounts,omitempty"`
}

type PortalMetadata struct {
	Service string `json:"service,omitempty"`
	// Set for DMs started from Matrix that don't exist on the Mac yet.
	NeedsCreate bool `json:"needs_create,omitempty"`
}

type MessageMetadata struct {
	TextHash       string `json:"text_hash,omitempty"`
	AttachmentGUID string `json:"attachment_guid,omitempty"`
}
