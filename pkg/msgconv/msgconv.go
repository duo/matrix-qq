package msgconv

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/format"
)

type MessageConverter struct {
	Bridge      *bridgev2.Bridge
	MaxFileSize int64
	HTMLParser  *format.HTMLParser
}

func NewMessageConverter(br *bridgev2.Bridge) *MessageConverter {
	mc := &MessageConverter{
		Bridge:      br,
		MaxFileSize: 100 * 1024 * 1024,
	}
	mc.HTMLParser = &format.HTMLParser{
		PillConverter: mc.convertPill,
		Newline:       "\n",
		TabsToSpaces:  4,
		BoldConverter: func(text string, ctx format.Context) string {
			return "*" + text + "*"
		},
		ItalicConverter: func(text string, ctx format.Context) string {
			return "_" + text + "_"
		},
		StrikethroughConverter: func(text string, ctx format.Context) string {
			return "~" + text + "~"
		},
		MonospaceConverter: func(text string, ctx format.Context) string {
			return "`" + text + "`"
		},
		MonospaceBlockConverter: func(code, language string, ctx format.Context) string {
			return "```\n" + code + "\n```"
		},
	}
	return mc
}
