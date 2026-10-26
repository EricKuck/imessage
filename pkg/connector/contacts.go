package connector

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-imessage/pkg/bluebubbles"
)

type contactCache struct {
	lock      sync.Mutex
	fetchedAt time.Time
	contacts  []bluebubbles.Contact
}

const minContactRefreshInterval = time.Minute

func (c *IMClient) getContacts(ctx context.Context) []bluebubbles.Contact {
	c.contacts.lock.Lock()
	defer c.contacts.lock.Unlock()
	interval := max(c.Main.Config.ContactRefreshInterval, minContactRefreshInterval)
	if time.Since(c.contacts.fetchedAt) < interval {
		return c.contacts.contacts
	}
	contacts, err := c.Client.GetContacts(ctx)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to fetch contacts")
		// Back off a little instead of hammering the server on every lookup.
		c.contacts.fetchedAt = time.Now().Add(minContactRefreshInterval - interval)
		return c.contacts.contacts
	}
	c.contacts.contacts = contacts
	c.contacts.fetchedAt = time.Now()
	return contacts
}

// findContact matches an address against the Mac's contacts. Phone numbers are
// compared by digits so formatting and country code differences don't matter.
func (c *IMClient) findContact(ctx context.Context, address string) *bluebubbles.Contact {
	address = normalizeAddress(address)
	digits := digitsOnly(address)
	var fallback *bluebubbles.Contact
	contacts := c.getContacts(ctx)
	for i := range contacts {
		contact := &contacts[i]
		if !contactMatches(contact, address, digits) {
			continue
		}
		// Contacts imported into BlueBubbles' own database take precedence.
		if contact.SourceType == "db" {
			return contact
		}
		if fallback == nil {
			fallback = contact
		}
	}
	return fallback
}

func contactMatches(contact *bluebubbles.Contact, address, digits string) bool {
	if strings.ContainsRune(address, '@') {
		for _, email := range contact.Emails {
			if strings.EqualFold(email.Address, address) {
				return true
			}
		}
		return false
	}
	if len(digits) < 7 {
		return false
	}
	for _, phone := range contact.PhoneNumbers {
		phoneDigits := digitsOnly(phone.Address)
		if len(phoneDigits) < 7 {
			continue
		}
		if strings.HasSuffix(digits, phoneDigits) || strings.HasSuffix(phoneDigits, digits) {
			return true
		}
	}
	return false
}

func contactName(contact *bluebubbles.Contact) string {
	if contact == nil {
		return ""
	}
	name := strings.TrimSpace(strings.TrimSpace(contact.FirstName) + " " + strings.TrimSpace(contact.LastName))
	if name == "" {
		name = strings.TrimSpace(contact.DisplayName)
	}
	if name == "" {
		name = strings.TrimSpace(contact.Nickname)
	}
	return name
}

func (c *IMClient) userInfoForAddress(ctx context.Context, address string) *bridgev2.UserInfo {
	address = normalizeAddress(address)
	contact := c.findContact(ctx, address)
	var identifiers []string
	if strings.ContainsRune(address, '@') {
		identifiers = append(identifiers, "mailto:"+address)
	} else {
		identifiers = append(identifiers, "tel:"+address)
	}
	info := &bridgev2.UserInfo{
		Identifiers: identifiers,
		Name:        new(c.Main.Config.FormatDisplayname(contactName(contact), address)),
		IsBot:       new(false),
	}
	if contact != nil && contact.Avatar != "" {
		data, err := base64.StdEncoding.DecodeString(contact.Avatar)
		if err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Str("address", address).Msg("Failed to decode contact avatar")
		} else if len(data) > 0 {
			info.Avatar = &bridgev2.Avatar{
				ID:  networkid.AvatarID(fmt.Sprintf("contact:%x", sha256.Sum256(data))),
				Get: func(context.Context) ([]byte, error) { return data, nil },
			}
		}
	} else {
		info.Avatar = &bridgev2.Avatar{Remove: true}
	}
	return info
}

func (c *IMClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	if c.IsThisUser(ctx, ghost.ID) {
		return nil, nil
	}
	return c.userInfoForAddress(ctx, string(ghost.ID)), nil
}

func (c *IMClient) GetContactList(ctx context.Context) ([]*bridgev2.ResolveIdentifierResponse, error) {
	var resp []*bridgev2.ResolveIdentifierResponse
	contacts := c.getContacts(ctx)
	for i := range contacts {
		contact := &contacts[i]
		var addresses []string
		for _, phone := range contact.PhoneNumbers {
			addresses = append(addresses, phone.Address)
		}
		for _, email := range contact.Emails {
			addresses = append(addresses, email.Address)
		}
		for _, address := range addresses {
			address = normalizeAddress(address)
			if !isPhoneNumber(address) && !isEmail(address) {
				continue
			}
			userID := makeUserID(address)
			ghost, err := c.Main.br.GetGhostByID(ctx, userID)
			if err != nil {
				return nil, fmt.Errorf("failed to get ghost %s: %w", userID, err)
			}
			resp = append(resp, &bridgev2.ResolveIdentifierResponse{
				Ghost:    ghost,
				UserID:   userID,
				UserInfo: c.userInfoForAddress(ctx, address),
			})
		}
	}
	return resp, nil
}
