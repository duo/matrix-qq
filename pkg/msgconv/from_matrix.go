package msgconv

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/duo/matrix-qq/pkg/qqid"

	"github.com/LagrangeDev/LagrangeGo/client"
	"github.com/LagrangeDev/LagrangeGo/message"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

func (mc *MessageConverter) ToQQ(
	ctx context.Context,
	client *client.QQClient,
	evt *event.Event,
	content *event.MessageEventContent,
	portal *bridgev2.Portal,
) ([]message.IMessageElement, error) {
	ctx = context.WithValue(ctx, contextKeyClient, client)
	ctx = context.WithValue(ctx, contextKeyPortal, portal)

	if evt.Type == event.EventSticker {
		content.MsgType = event.MessageType(event.EventSticker.Type)
	}

	elements := []message.IMessageElement{}

	switch content.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		elements = append(elements, mc.constructTextMessage(ctx, content)...)
	case event.MessageType(event.EventSticker.Type), event.MsgImage, event.MsgVideo, event.MsgAudio:
		data, err := mc.Bridge.Bot.DownloadMedia(ctx, content.URL, content.File)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", bridgev2.ErrMediaDownloadFailed, err)
		}
		elements = append(elements, mc.constructMediaMessage(ctx, content, data)...)
	case event.MsgLocation:
		lat, lng, err := parseGeoURI(content.GeoURI)
		if err != nil {
			return nil, err
		}
		elements = append(elements, mc.constructLocationMessage(ctx, content.Body, lat, lng)...)
	default:
		return nil, fmt.Errorf("%w %s", bridgev2.ErrUnsupportedMessageType, content.MsgType)

	}

	return elements, nil
}

func (mc *MessageConverter) constructTextMessage(ctx context.Context, content *event.MessageEventContent) []message.IMessageElement {
	text, mentions := mc.parseText(ctx, content)
	if content.Mentions != nil && content.Mentions.Room {
		mentions = append(mentions, "room")
	}

	if len(mentions) == 0 {
		return []message.IMessageElement{message.NewText(text)}
	}

	keywords := make([]string, len(mentions))
	for i, m := range mentions {
		keywords[i] = "@" + m
	}

	pattern := strings.Join(keywords, "|")
	re := regexp.MustCompile("(?:" + pattern + ")")

	parts := re.Split(text, -1)
	matches := re.FindAllString(text, -1)

	var splits []string
	for i := 0; i < len(parts); i++ {
		if parts[i] != "" {
			splits = append(splits, parts[i])
		}
		if i < len(matches) {
			splits = append(splits, matches[i])
		}
	}

	elems := []message.IMessageElement{}
	for _, s := range splits {
		if slices.Contains(keywords, s) {
			if s == "@room" {
				elems = append(elems, message.NewAt(0))
			} else {
				uin, _ := strconv.ParseUint(string(s[1:]), 10, 32)
				elems = append(elems, message.NewAt(uint32(uin)))
			}
		} else {
			elems = append(elems, message.NewText(s))
		}
	}

	return elems
}

func (mc *MessageConverter) constructMediaMessage(_ context.Context, content *event.MessageEventContent, data []byte) []message.IMessageElement {
	fileName := content.Body
	if content.FileName != "" {
		fileName = content.FileName
	}

	switch content.MsgType {
	case event.MessageType(event.EventSticker.Type), event.MsgImage:
		return []message.IMessageElement{message.NewImage(data)}
	case event.MsgVideo:
		return []message.IMessageElement{message.NewVideo(data, qqid.SmallestImg)}
	case event.MsgAudio:
		// TODO: ogg to silk
		return []message.IMessageElement{message.NewRecord(data)}
	case event.MsgFile:
		// FIXME:
		return []message.IMessageElement{message.NewFile(data, fileName)}
	}

	return []message.IMessageElement{}
}

func (mc *MessageConverter) constructLocationMessage(_ context.Context, name string, lat, lng float64) []message.IMessageElement {
	locationJson := fmt.Sprintf(`
		{
			"app": "com.tencent.map",
			"desc": "地图",
			"view": "LocationShare",
			"ver": "0.0.0.1",
			"prompt": "[位置]%s",
			"from": 1,
			"meta": {
			  "Location.Search": {
				"id": "12250896297164027526",
				"name": "%s",
				"address": "%s",
				"lat": "%.5f",
				"lng": "%.5f",
				"from": "plusPanel"
			  }
			},
			"config": {
			  "forward": 1,
			  "autosize": 1,
			  "type": "card"
			}
		}
		`, name, name, name, lat, lng)

	return []message.IMessageElement{message.NewLightApp(locationJson)}
}

func (mc *MessageConverter) parseText(ctx context.Context, content *event.MessageEventContent) (text string, mentions []string) {
	mentions = make([]string, 0)

	parseCtx := format.NewContext(ctx)
	parseCtx.ReturnData["allowed_mentions"] = content.Mentions
	parseCtx.ReturnData["output_mentions"] = &mentions
	if content.Format == event.FormatHTML {
		text = mc.HTMLParser.Parse(content.FormattedBody, parseCtx)
	} else {
		text = content.Body
	}
	return
}

func (mc *MessageConverter) convertPill(displayname, mxid, eventID string, ctx format.Context) string {
	if len(mxid) == 0 || mxid[0] != '@' {
		return format.DefaultPillConverter(displayname, mxid, eventID, ctx)
	}
	allowedMentions, _ := ctx.ReturnData["allowed_mentions"].(*event.Mentions)
	if allowedMentions != nil && !allowedMentions.Has(id.UserID(mxid)) {
		return displayname
	}
	var oid string
	ghost, err := mc.Bridge.GetGhostByMXID(ctx.Ctx, id.UserID(mxid))
	if err != nil {
		zerolog.Ctx(ctx.Ctx).Err(err).Str("mxid", mxid).Msg("Failed to get ghost for mention")
		return displayname
	} else if ghost != nil {
		oid = string(ghost.ID)
	} else if user, err := mc.Bridge.GetExistingUserByMXID(ctx.Ctx, id.UserID(mxid)); err != nil {
		zerolog.Ctx(ctx.Ctx).Err(err).Str("mxid", mxid).Msg("Failed to get user for mention")
		return displayname
	} else if user != nil {
		portal := getPortal(ctx.Ctx)
		login, _, _ := portal.FindPreferredLogin(ctx.Ctx, user, false)
		if login == nil {
			return displayname
		}
		oid = string(login.ID)
	} else {
		return displayname
	}
	mentions := ctx.ReturnData["output_mentions"].(*[]string)
	*mentions = append(*mentions, oid)
	return fmt.Sprintf("@%s", oid)
}

func parseGeoURI(uri string) (lat, lng float64, err error) {
	if !strings.HasPrefix(uri, "geo:") {
		err = fmt.Errorf("uri doesn't have geo: prefix")
		return
	}
	// Remove geo: prefix and anything after ;
	coordinates := strings.Split(strings.TrimPrefix(uri, "geo:"), ";")[0]

	if splitCoordinates := strings.Split(coordinates, ","); len(splitCoordinates) != 2 {
		err = fmt.Errorf("didn't find exactly two numbers separated by a comma")
	} else if lat, err = strconv.ParseFloat(splitCoordinates[0], 64); err != nil {
		err = fmt.Errorf("latitude is not a number: %w", err)
	} else if lng, err = strconv.ParseFloat(splitCoordinates[1], 64); err != nil {
		err = fmt.Errorf("longitude is not a number: %w", err)
	}
	return
}
