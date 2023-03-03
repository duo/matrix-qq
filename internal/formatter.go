package internal

import (
	"github.com/duo/matrix-qq/internal/types"

	"github.com/Mrs4s/MiraiGo/message"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

const qqElementsContextKey = "me.lxduo.qq.elements"

type Formatter struct {
	bridge *QQBridge

	matrixHTMLParser *format.HTMLParser
}

func NewFormatter(br *QQBridge) *Formatter {
	formatter := &Formatter{
		bridge: br,
		matrixHTMLParser: &format.HTMLParser{
			TabsToSpaces: 4,
			Newline:      "\n",
			PillConverter: func(displayname, mxid, eventID string, ctx format.Context) string {
				if mxid[0] == '@' {
					puppet := br.GetPuppetByMXID(id.UserID(mxid))
					if puppet != nil {
						elems, ok := ctx.ReturnData[qqElementsContextKey].([]message.IMessageElement)
						if ok {
							content := elems[len(elems)-1].(*message.TextElement).Content
							modifiedContent := content[:len(content)-len(displayname)]
							elems[len(elems)-1] = message.NewText(modifiedContent)
							ctx.ReturnData[qqElementsContextKey] = append(elems, message.NewAt(
								puppet.UID.IntUin(), "@"+puppet.UID.Uin,
							))
						}
						return "@" + puppet.UID.Uin
					}
				}
				return mxid
			},
			TextConverter: func(text string, ctx format.Context) string {
				elems, ok := ctx.ReturnData[qqElementsContextKey].([]message.IMessageElement)
				if !ok {
					ctx.ReturnData[qqElementsContextKey] = []message.IMessageElement{message.NewText(text)}
				} else {
					lastElem := elems[len(elems)-1]
					if e, ok := lastElem.(*message.TextElement); ok {
						elems[len(elems)-1] = message.NewText(e.Content + text)
						ctx.ReturnData[qqElementsContextKey] = elems
					} else {
						ctx.ReturnData[qqElementsContextKey] = append(elems, message.NewText(text))
					}
				}
				return text
			},
		},
	}

	return formatter
}

func (f *Formatter) GetMatrixInfoByUID(roomID id.RoomID, uid types.UID) (id.UserID, string) {
	var mxid id.UserID
	var displayname string
	if puppet := f.bridge.GetPuppetByUID(uid); puppet != nil {
		mxid = puppet.MXID
		displayname = puppet.Displayname
	}
	if user := f.bridge.GetUserByUID(uid); user != nil {
		mxid = user.MXID
		member := f.bridge.StateStore.GetMember(roomID, user.MXID)
		if len(member.Displayname) > 0 {
			displayname = member.Displayname
		}
	}

	return mxid, displayname
}

func (f *Formatter) ParseMatrix(html string) []message.IMessageElement {
	ctx := format.NewContext()
	f.matrixHTMLParser.Parse(html, ctx)

	if elems, ok := ctx.ReturnData[qqElementsContextKey]; ok {
		return elems.([]message.IMessageElement)
	} else {
		return []message.IMessageElement{}
	}
}
