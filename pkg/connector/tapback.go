package connector

// iMessage tapbacks as BlueBubbles names them, with the emoji shown on Matrix.
var tapbackEmojis = map[string]string{
	"love":      "❤️",
	"like":      "\U0001f44d",
	"dislike":   "\U0001f44e",
	"laugh":     "\U0001f602",
	"emphasize": "‼️",
	"question":  "❓",
}

var tapbackOrder = []string{"love", "like", "dislike", "laugh", "emphasize", "question"}

func allTapbackEmojis() []string {
	out := make([]string, len(tapbackOrder))
	for i, name := range tapbackOrder {
		out[i] = tapbackEmojis[name]
	}
	return out
}

func tapbackEmoji(name string) string {
	return tapbackEmojis[name]
}

// tapbackFromEmoji maps a Matrix reaction to a tapback name, accepting the
// common emoji variants iMessage users expect. Returns "" if there's no match.
func tapbackFromEmoji(emoji string) string {
	runes := []rune(emoji)
	if len(runes) == 0 {
		return ""
	}
	switch runes[0] {
	case '♥', '❤', '\U0001f499', '\U0001f49a', '\U0001f90e', '\U0001f5a4', '\U0001f90d', '\U0001f9e1',
		'\U0001f49b', '\U0001f49c', '\U0001f496', '❣', '\U0001f495', '\U0001f49f':
		return "love"
	case '\U0001f44d':
		return "like"
	case '\U0001f44e':
		return "dislike"
	case '\U0001f602', '\U0001f639', '\U0001f606', '\U0001f923':
		return "laugh"
	case '❕', '❗', '‼':
		return "emphasize"
	case '❓', '❔':
		return "question"
	default:
		return ""
	}
}
