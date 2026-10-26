package connector

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

func (c *IMClient) makePortalKey(chatGUID string) networkid.PortalKey {
	return networkid.PortalKey{ID: networkid.PortalID(chatGUID), Receiver: c.UserLogin.ID}
}

func (c *IMClient) selfUserID() networkid.UserID {
	return networkid.UserID(c.UserLogin.ID)
}

func makeUserID(address string) networkid.UserID {
	return networkid.UserID(normalizeAddress(address))
}

func normalizeAddress(address string) string {
	address = strings.TrimSpace(address)
	if strings.ContainsRune(address, '@') {
		return strings.ToLower(address)
	}
	return address
}

func isPhoneNumber(address string) bool {
	if len(address) < 4 || !strings.HasPrefix(address, "+") {
		return false
	}
	for _, r := range address[1:] {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func isEmail(address string) bool {
	at := strings.IndexRune(address, '@')
	return at > 0 && at < len(address)-1 && !strings.ContainsAny(address, " /")
}

func digitsOnly(s string) string {
	var out strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func makeMessageID(guid string) networkid.MessageID {
	return networkid.MessageID(guid)
}

func makePartID(index int) networkid.PartID {
	return networkid.PartID(strconv.Itoa(index))
}

func parsePartID(id networkid.PartID) int {
	index, err := strconv.Atoi(string(id))
	if err != nil {
		return 0
	}
	return index
}

// parseAssociatedGUID parses tapback and sticker targets, which look like
// "p:0/GUID" (part 0 of the message) or "bp:GUID" (the whole message).
func parseAssociatedGUID(target string) (guid string, part int) {
	if rest, ok := strings.CutPrefix(target, "bp:"); ok {
		return rest, 0
	}
	if rest, ok := strings.CutPrefix(target, "p:"); ok {
		partStr, guid, found := strings.Cut(rest, "/")
		if found {
			part, _ = strconv.Atoi(partStr)
			return guid, part
		}
	}
	return target, 0
}

// parseThreadOriginatorPart extracts the part index from values like "0:0:17".
func parseThreadOriginatorPart(part string) int {
	first, _, _ := strings.Cut(part, ":")
	index, _ := strconv.Atoi(first)
	return index
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
