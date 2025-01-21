package msgconv

import (
	"context"
	"fmt"
	"html"
	"math"
	"strings"

	"github.com/duo/matrix-qq/pkg/qqid"

	"github.com/LagrangeDev/LagrangeGo/client"
	"github.com/LagrangeDev/LagrangeGo/message"
	"github.com/antchfx/xmlquery"
	"github.com/gabriel-vasile/mimetype"
	"github.com/rs/zerolog"
	"github.com/tidwall/gjson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

type contextKey int

const (
	contextKeyClient contextKey = iota
	contextKeyIntent
	contextKeyPortal
)

func (mc *MessageConverter) ToMatrix(
	ctx context.Context,
	client *client.QQClient,
	portal *bridgev2.Portal,
	intent bridgev2.MatrixAPI,
	msg *qqid.Message,
) *bridgev2.ConvertedMessage {
	ctx = context.WithValue(ctx, contextKeyClient, client)
	ctx = context.WithValue(ctx, contextKeyIntent, intent)
	ctx = context.WithValue(ctx, contextKeyPortal, portal)

	var part *bridgev2.ConvertedMessagePart

	switch msg.Type {
	case qqid.MsgImage:
		part = mc.convertImageMessage(ctx, msg)
	case qqid.MsgAudio:
		part = mc.convertMediaMessage(ctx, msg)[0]
	case qqid.MsgVideo:
		part = mc.convertMediaMessage(ctx, msg)[0]
	case qqid.MsgFile:
		part = mc.convertMediaMessage(ctx, msg)[0]
	case qqid.MsgApp:
		part = mc.convertAppMessage(ctx, msg)
	case qqid.MsgRevoke:
		part = mc.convertRevokeMessage(ctx, msg)
	case qqid.MsgSticker:
	case qqid.MsgLocation:
	default:
		part = mc.convertTextMessage(ctx, msg)
	}

	// Mentions
	part.Content.Mentions = &event.Mentions{}
	mc.addMentions(ctx, msg.Elements, part.Content)

	cm := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{part},
	}

	// ReplyTo
	if msg.Type == qqid.MsgRevoke {
		cm.ReplyTo = &networkid.MessageOptionalPartID{
			MessageID: qqid.MakeMessageID(msg.ChatID, msg.ID),
		}
	} else if v, ok := msg.Elements[0].(*message.ReplyElement); ok {
		cm.ReplyTo = &networkid.MessageOptionalPartID{
			MessageID: qqid.MakeMessageID(msg.ChatID, fmt.Sprint(v.ReplySeq)),
		}
	}

	return cm
}

func (mc *MessageConverter) convertTextMessage(_ context.Context, msg *qqid.Message) *bridgev2.ConvertedMessagePart {
	return &bridgev2.ConvertedMessagePart{
		Type: event.EventMessage,
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    toContent(msg.Elements),
		},
	}
}

func (mc *MessageConverter) convertImageMessage(ctx context.Context, msg *qqid.Message) *bridgev2.ConvertedMessagePart {
	parts := mc.convertMediaMessage(ctx, msg)
	if len(parts) == 1 {
		return parts[0]
	}

	var imagesMarkdown strings.Builder
	for _, part := range parts {
		fmt.Fprintf(&imagesMarkdown, "![%s](%s)\n", part.Content.FileName, part.Content.URL)
	}

	rendered := format.RenderMarkdown(imagesMarkdown.String(), true, false)
	content := toContent(msg.Elements)

	return &bridgev2.ConvertedMessagePart{
		Type: event.EventMessage,
		Content: &event.MessageEventContent{
			MsgType:       event.MsgText,
			Format:        event.FormatHTML,
			Body:          content,
			FormattedBody: fmt.Sprintf("%s\n%s", rendered.FormattedBody, content),
		},
	}
}

func (mc *MessageConverter) convertMediaMessage(ctx context.Context, msg *qqid.Message) []*bridgev2.ConvertedMessagePart {
	parts := make([]*bridgev2.ConvertedMessagePart, 0)

	for _, elem := range msg.Elements {
		elemType := elem.Type()
		if elemType == message.Image || elemType == message.Voice ||
			elemType == message.Video || elemType == message.File {
			if part, err := mc.reploadAttachment(ctx, elem); err != nil {
				parts = append(parts, mc.makeMediaFailure(ctx, err))
			} else {
				parts = append(parts, part)
			}
		}
	}

	return parts
}

func (mc *MessageConverter) convertAppMessage(_ context.Context, msg *qqid.Message) *bridgev2.ConvertedMessagePart {
	// XML
	if v, ok := msg.Elements[0].(*message.XMLElement); ok {
		body := v.Content

		var content strings.Builder
		if doc, err := xmlquery.Parse(strings.NewReader(v.Content)); err == nil {
			if action := xmlquery.FindOne(doc, "//msg[@action='viewMultiMsg']"); action != nil {
				items := xmlquery.Find(doc, "//item")
				for _, item := range items {
					titles := xmlquery.Find(item, "title")
					for _, title := range titles {
						fmt.Fprintf(&content, "%s\n", title.InnerText())
					}
				}

				if content.Len() > 0 {
					body = content.String()
				}
			}
		}

		return &bridgev2.ConvertedMessagePart{
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				Body:    body,
				MsgType: event.MsgText,
			},
		}
	}

	// JSON
	content := msg.Elements[0].(*message.LightAppElement).Content

	var title string
	var desc string
	var url string

	view := gjson.Get(content, "view").String()
	if view == "LocationShare" {
		name := gjson.Get(content, "meta.*.name").String()
		address := gjson.Get(content, "meta.*.address").String()
		latitude := gjson.Get(content, "meta.*.lat").Float()
		longitude := gjson.Get(content, "meta.*.lng").Float()

		return mc.convertLocationMessage(name, address, latitude, longitude)
	} else {
		if url = gjson.Get(content, "meta.*.qqdocurl").String(); len(url) > 0 {
			desc = gjson.Get(content, "meta.*.desc").String()
			title = gjson.Get(content, "prompt").String()
		} else if url = gjson.Get(content, "meta.*.jumpUrl").String(); len(url) > 0 {
			desc = gjson.Get(content, "meta.*.desc").String()
			title = gjson.Get(content, "prompt").String()
		}
	}

	body := fmt.Sprintf("%s\n\n%s\n\n%s", title, desc, url)
	rendered := format.RenderMarkdown(
		fmt.Sprintf("**%s**\n%s\n\n[%s](%s)", title, desc, url, url),
		true,
		false,
	)

	return &bridgev2.ConvertedMessagePart{
		Type: event.EventMessage,
		Content: &event.MessageEventContent{
			Body:          body,
			MsgType:       event.MsgText,
			Format:        event.FormatHTML,
			FormattedBody: rendered.FormattedBody,
		},
	}
}

func (mc *MessageConverter) convertLocationMessage(name, address string, lat, lng float64) *bridgev2.ConvertedMessagePart {
	url := fmt.Sprintf("https://maps.google.com/?q=%.5f,%.5f", lat, lng)
	if len(name) == 0 {
		latChar := 'N'
		if lat < 0 {
			latChar = 'S'
		}
		longChar := 'E'
		if lng < 0 {
			longChar = 'W'
		}
		name = fmt.Sprintf("%.4f° %c %.4f° %c", math.Abs(lat), latChar, math.Abs(lng), longChar)
	}

	content := &event.MessageEventContent{
		MsgType:       event.MsgLocation,
		Body:          fmt.Sprintf("Location: %s\n%s\n%s", name, address, url),
		Format:        event.FormatHTML,
		FormattedBody: fmt.Sprintf("Location: <a href='%s'>%s</a><br>%s", url, name, address),
		GeoURI:        fmt.Sprintf("geo:%.5f,%.5f", lat, lng),
	}

	return &bridgev2.ConvertedMessagePart{
		Type:    event.EventMessage,
		Content: content,
	}
}

func (mc *MessageConverter) convertRevokeMessage(_ context.Context, _ *qqid.Message) *bridgev2.ConvertedMessagePart {
	return &bridgev2.ConvertedMessagePart{
		Type: event.EventMessage,
		Content: &event.MessageEventContent{
			MsgType:       event.MsgNotice,
			Format:        event.FormatHTML,
			Body:          "revoke message",
			FormattedBody: "<del>revoke message</del>",
		},
	}
}

func (mc *MessageConverter) reploadAttachment(ctx context.Context, elem message.IMessageElement) (*bridgev2.ConvertedMessagePart, error) {
	var data []byte
	var err error
	var fileName string

	content := &event.MessageEventContent{
		Info: &event.FileInfo{},
	}

	switch v := elem.(type) {
	case *message.ImageElement:
		data, err = qqid.GetBytes(v.URL)
		fileName = v.FileUUID
		content.MsgType = event.MsgImage
	case *message.VoiceElement:
		// TODO: silk to ogg
		data, err = qqid.GetBytes(v.URL)
		fileName = v.Name
		content.MsgType = event.MsgAudio
		content.MSC3245Voice = &event.MSC3245Voice{}
	case *message.ShortVideoElement:
		data, err = qqid.GetBytes(v.URL)
		fileName = v.Name
		content.MsgType = event.MsgVideo
	case *message.FileElement:
		data, err = qqid.GetBytes(v.FileURL)
		fileName = v.FileName
		content.MsgType = event.MsgFile
	}

	if err != nil {
		return nil, fmt.Errorf("failed to download attachment: %w", err)
	}

	mime := mimetype.Detect(data)
	content.Info.Size = len(data)
	content.FileName = fileName + mime.Extension()

	content.URL, content.File, err = getIntent(ctx).UploadMedia(ctx, getPortal(ctx).MXID, data, fileName, mime.String())
	if err != nil {
		return nil, err
	}

	//content.Body = fileName
	content.Info.MimeType = mime.String()

	return &bridgev2.ConvertedMessagePart{
		Type:    event.EventMessage,
		Content: content,
	}, nil
}

func (mc *MessageConverter) makeMediaFailure(ctx context.Context, err error) *bridgev2.ConvertedMessagePart {
	zerolog.Ctx(ctx).Err(err).Msg("Failed to reupload QQ attachment")
	return &bridgev2.ConvertedMessagePart{
		Type: event.EventMessage,
		Content: &event.MessageEventContent{
			MsgType: event.MsgNotice,
			Body:    fmt.Sprintf("Failed to upload QQ attachment: %v", err),
		},
	}
}

func (mc *MessageConverter) addMentions(ctx context.Context, elems []message.IMessageElement, into *event.MessageEventContent) {
	if len(elems) == 0 {
		return
	}

	mentionedID := make([]string, 0)
	for _, elem := range elems {
		if v, ok := elem.(*message.AtElement); ok {
			if v.TargetUin == 0 {
				mentionedID = append(mentionedID, "room")
			} else {
				mentionedID = append(mentionedID, fmt.Sprint(v.TargetUin))
			}
		}
	}

	// Remove first reply mention id
	if _, ok := elems[0].(*message.ReplyElement); ok {
		mentionedID = mentionedID[1:]
	}

	if len(mentionedID) == 0 {
		return
	}

	into.EnsureHasHTML()

	for _, id := range mentionedID {
		if id == "room" {
			into.Mentions.Room = true
			continue
		}

		// TODO: get group nickname
		mxid, displayname, err := mc.getBasicUserInfo(ctx, qqid.MakeUserID(id))
		if err != nil {
			zerolog.Ctx(ctx).Err(err).Str("id", id).Msg("Failed to get user info")
			continue
		}
		into.Mentions.UserIDs = append(into.Mentions.UserIDs, mxid)
		mentionText := "@" + id
		into.Body = strings.ReplaceAll(into.Body, mentionText, displayname)
		into.FormattedBody = strings.ReplaceAll(into.FormattedBody, mentionText, fmt.Sprintf(`<a href="%s">%s</a>`, mxid.URI().MatrixToURL(), html.EscapeString(displayname)))
	}
}

func (mc *MessageConverter) getBasicUserInfo(ctx context.Context, user networkid.UserID) (id.UserID, string, error) {
	ghost, err := mc.Bridge.GetGhostByID(ctx, user)
	if err != nil {
		return "", "", fmt.Errorf("failed to get ghost by ID: %w", err)
	}
	login := mc.Bridge.GetCachedUserLoginByID(networkid.UserLoginID(user))
	if login != nil {
		return login.UserMXID, ghost.Name, nil
	}
	return ghost.Intent.GetMXID(), ghost.Name, nil
}

func toContent(elems []message.IMessageElement) string {
	var content strings.Builder

	mentionIndex := 0
	for _, elem := range elems {
		switch e := elem.(type) {
		case *message.ReplyElement:
		case *message.TextElement:
			fmt.Fprint(&content, e.Content)
		case *message.LightAppElement:
			fmt.Fprint(&content, e.Content)
		case *message.XMLElement:
			fmt.Fprint(&content, e.Content)
		case *message.AtElement:
			mentionIndex++
			// Skip first reply mention
			if _, ok := elems[0].(*message.ReplyElement); ok && mentionIndex == 1 {
				continue
			}
			if e.TargetUin == 0 {
				fmt.Fprint(&content, "@room")
			} else {
				fmt.Fprintf(&content, "@%d", e.TargetUin)
			}
		case *message.ForwardMessage:
			fmt.Fprintf(&content, "[Forward: %s]", e.ResID)
		case *message.FaceElement:
			fmt.Fprintf(&content, "/[Face%d]", e.FaceID)
		case *message.ImageElement:
			fmt.Fprintf(&content, "[Image]")
		case *message.VoiceElement:
			fmt.Fprintf(&content, "[Voice]")
		case *message.ShortVideoElement:
			fmt.Fprintf(&content, "[Video]")
		case *message.FileElement:
			fmt.Fprintf(&content, "[File]")
		}
	}

	return content.String()
}

/*
func getClient(ctx context.Context) *client.QQClient {
	return ctx.Value(contextKeyClient).(*client.QQClient)
}
*/

func getIntent(ctx context.Context) bridgev2.MatrixAPI {
	return ctx.Value(contextKeyIntent).(bridgev2.MatrixAPI)
}

func getPortal(ctx context.Context) *bridgev2.Portal {
	return ctx.Value(contextKeyPortal).(*bridgev2.Portal)
}
