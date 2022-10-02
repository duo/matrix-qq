package internal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/duo/matrix-qq/internal/database"
	"github.com/duo/matrix-qq/internal/types"

	"github.com/Mrs4s/MiraiGo/client"
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/gabriel-vasile/mimetype"
	"github.com/tidwall/gjson"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/bridge"
	"maunium.net/go/mautrix/bridge/bridgeconfig"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
	"maunium.net/go/mautrix/util"
	"maunium.net/go/mautrix/util/dbutil"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	log "maunium.net/go/maulogger/v2"
)

const (
	PrivateChatTopic      = "QQ private chat"
	recentlyHandledLength = 100
)

var (
	ErrStatusBroadcastDisabled = errors.New("status bridging is disabled")
	errUserNotConnected        = errors.New("you are not connected to QQ")
	errDifferentUser           = errors.New("user is not the recipient of this private chat portal")
	errUserNotLoggedIn         = errors.New("user is not logged in")
	errMediaDownloadFailed     = errors.New("failed to download media")
	errMediaDecryptFailed      = errors.New("failed to decrypt media")

	PortalCreationDummyEvent = event.Type{Type: "me.lxduo.qq.dummy.portal_created", Class: event.MessageEventType}
)

type PortalMessage struct {
	private *message.PrivateMessage
	group   *message.GroupMessage
	temp    *message.TempMessage
	offline *client.OfflineFileEvent
	fake    *fakeMessage
	source  *User
}

type PortalMatrixMessage struct {
	evt        *event.Event
	user       *User
	receivedAt time.Time
}

type Portal struct {
	*database.Portal

	bridge *QQBridge
	log    log.Logger

	roomCreateLock sync.Mutex
	encryptLock    sync.Mutex
	avatarLock     sync.Mutex

	recentlyHandled      [recentlyHandledLength]recentlyHandledWrapper
	recentlyHandledLock  sync.Mutex
	recentlyHandledIndex uint8

	messages       chan PortalMessage
	matrixMessages chan PortalMatrixMessage
}

type ReplyInfo struct {
	ReplySeq int32
	Time     int32
	Sender   types.UID
}

type ConvertedMessage struct {
	Intent  *appservice.IntentAPI
	Type    event.Type
	Content *event.MessageEventContent
	Extra   map[string]interface{}
	Caption *event.MessageEventContent

	ReplyTo  *ReplyInfo
	Error    database.MessageErrorType
	MediaKey []byte
}

type fakeMessage struct {
	Sender    types.UID
	Text      string
	ID        string
	Time      time.Time
	Important bool
}

type recentlyHandledWrapper struct {
	id  string
	err database.MessageErrorType
}

func (p *Portal) IsEncrypted() bool {
	return p.Encrypted
}

func (p *Portal) MarkEncrypted() {
	p.Encrypted = true
	p.Update(nil)
}

func (p *Portal) ReceiveMatrixEvent(user bridge.User, evt *event.Event) {
	if user.GetPermissionLevel() >= bridgeconfig.PermissionLevelUser {
		p.matrixMessages <- PortalMatrixMessage{user: user.(*User), evt: evt, receivedAt: time.Now()}
	}
}

func (p *Portal) GetUsers() []*User {
	return nil
}

func (p *Portal) handleQQMessageLoopItem(msg PortalMessage) {
	defer func() {
		panicErr := recover()
		if panicErr != nil {
			p.log.Warnfln("Panic while process %+v: %v\n%s", msg, panicErr, debug.Stack())
		}
	}()

	if len(p.MXID) == 0 {
		if (msg.fake == nil && msg.private == nil && msg.group == nil && msg.temp == nil && msg.offline == nil) ||
			(msg.private != nil && !containsSupportedMessage(msg.private.Elements)) ||
			(msg.group != nil && !containsSupportedMessage(msg.group.Elements)) ||
			(msg.temp != nil && !containsSupportedMessage(msg.temp.Elements)) {
			p.log.Debugln("Not creating portal room for incoming message: message is not a chat message")
			return
		}
		p.log.Debugln("Creating Matrix room from incoming message")
		err := p.CreateMatrixRoom(msg.source, nil, false)
		if err != nil {
			p.log.Errorln("Failed to create portal room:", err)

			return
		}
	}

	switch {
	case msg.private != nil, msg.group != nil, msg.temp != nil, msg.offline != nil:
		p.handleQQMessage(msg.source, msg)
	case msg.fake != nil:
		msg.fake.ID = "FAKE::" + msg.fake.ID
		p.handleFakeMessage(*msg.fake)
	default:
		p.log.Warnln("Unexpected PortalMessage with no message: %+v", msg)
	}
}

func (p *Portal) handleMatrixMessageLoopItem(msg PortalMatrixMessage) {
	defer func() {
		panicErr := recover()
		if panicErr != nil {
			p.log.Warnfln("Panic while process %+v: %v\n%s", msg, panicErr, debug.Stack())
		}
	}()

	switch msg.evt.Type {
	case event.EventMessage, event.EventSticker:
		p.HandleMatrixMessage(msg.user, msg.evt)
	case event.EventRedaction:
		p.HandleMatrixRedaction(msg.user, msg.evt)
	case event.EventReaction:
		p.HandleMatrixReaction(msg.user, msg.evt)
	default:
		p.log.Warnln("Unsupported event type %+v in portal message channel", msg.evt.Type)
	}
}

func (p *Portal) handleMessageLoop() {
	for {
		select {
		case msg := <-p.messages:
			p.handleQQMessageLoopItem(msg)
		case msg := <-p.matrixMessages:
			p.handleMatrixMessageLoopItem(msg)
		}
	}
}

func containsSupportedMessage(elems []message.IMessageElement) bool {
	if len(elems) > 0 {
		for _, mp := range elems {
			switch mp.(type) {
			case
				*message.TextElement, *message.FaceElement, *message.AtElement,
				*message.FriendImageElement, *message.GroupImageElement, *message.ShortVideoElement,
				*message.GroupFileElement, *message.VoiceElement, *message.ReplyElement,
				*message.LightAppElement:
				return true
			}
		}
	}
	return false
}

func (p *Portal) handleFakeMessage(msg fakeMessage) {
	msgKey := database.NewFakeKey(msg.ID)
	if p.isRecentlyHandled(msg.ID, database.MsgNoError) {
		p.log.Debugfln("Not handling %s (fake): message was recently handled", msg.ID)
		return
	} else if existingMsg := p.bridge.DB.Message.GetByMessageKey(p.Key, msgKey); existingMsg != nil {
		p.log.Debugfln("Not handling %s (fake): message is duplicate", msg.ID)
		return
	}

	intent := p.bridge.GetPuppetByUID(msg.Sender).IntentFor(p)
	if !intent.IsCustomPuppet && p.IsPrivateChat() && msg.Sender.Uin == p.Key.Receiver.Uin {
		p.log.Debugfln("Not handling %s (fake): user doesn't have double puppeting enabled", msg.ID)
		return
	}

	msgType := event.MsgNotice
	if msg.Important {
		msgType = event.MsgText
	}

	resp, err := p.sendMessage(intent, event.EventMessage, &event.MessageEventContent{
		MsgType: msgType,
		Body:    msg.Text,
	}, nil, msg.Time.UnixMilli())
	if err != nil {
		p.log.Errorfln("Failed to send %s to Matrix: %v", msg.ID, err)
	} else {
		p.finishHandling(nil, msgKey, msg.Time, msg.Sender, resp.EventID, database.MsgFake, database.MsgNoError, "")
	}
}

func (p *Portal) convertQQVideo(source *User, msgKey database.MessageKey, elem *message.ShortVideoElement, intent *appservice.IntentAPI) *ConvertedMessage {
	content := &event.MessageEventContent{
		MsgType: event.MsgVideo,
		Info: &event.FileInfo{
			MimeType: "application/octet-stream",
			Size:     int(elem.Size),
		},
		Body: "video",
	}

	converted := &ConvertedMessage{
		Intent:  intent,
		Type:    event.EventMessage,
		Content: content,
	}

	url := source.Client.GetShortVideoUrl(elem.Uuid, elem.Md5)
	data, mime, err := download(url)
	if err != nil {
		return p.makeMediaBridgeFailureMessage(msgKey, errors.New("failed to download video from QQ"), converted)
	}

	content.Info.MimeType = mime.String()
	content.Body = mime.String()

	err = p.uploadMedia(intent, data, content)
	if err != nil {
		if errors.Is(err, mautrix.MTooLarge) {
			return p.makeMediaBridgeFailureMessage(msgKey, errors.New("homeserver rejected too large file"), converted)
		} else if httpErr, ok := err.(mautrix.HTTPError); ok && httpErr.IsStatus(413) {
			return p.makeMediaBridgeFailureMessage(msgKey, errors.New("proxy rejected too large file"), converted)
		} else {
			return p.makeMediaBridgeFailureMessage(msgKey, fmt.Errorf("failed to upload media: %w", err), converted)
		}
	}

	return converted
}

func (p *Portal) convertQQFile(source *User, msgKey database.MessageKey, e *client.OfflineFileEvent, intent *appservice.IntentAPI) *ConvertedMessage {
	content := &event.MessageEventContent{
		MsgType: event.MsgFile,
		Info: &event.FileInfo{
			MimeType: "application/octet-stream",
			Size:     int(e.FileSize),
		},
		Body: e.FileName,
	}

	converted := &ConvertedMessage{
		Intent:  intent,
		Type:    event.EventMessage,
		Content: content,
	}

	data, mime, err := download(e.DownloadUrl)
	if err != nil {
		return p.makeMediaBridgeFailureMessage(msgKey, errors.New("failed to download file from QQ"), converted)
	}

	content.Info.MimeType = mime.String()

	err = p.uploadMedia(intent, data, content)
	if err != nil {
		if errors.Is(err, mautrix.MTooLarge) {
			return p.makeMediaBridgeFailureMessage(msgKey, errors.New("homeserver rejected too large file"), converted)
		} else if httpErr, ok := err.(mautrix.HTTPError); ok && httpErr.IsStatus(413) {
			return p.makeMediaBridgeFailureMessage(msgKey, errors.New("proxy rejected too large file"), converted)
		} else {
			return p.makeMediaBridgeFailureMessage(msgKey, fmt.Errorf("failed to upload media: %w", err), converted)
		}
	}

	return converted
}

func (p *Portal) convertQQGroupFile(source *User, msgKey database.MessageKey, elem *message.GroupFileElement, intent *appservice.IntentAPI) *ConvertedMessage {
	content := &event.MessageEventContent{
		MsgType: event.MsgFile,
		Info: &event.FileInfo{
			MimeType: "application/octet-stream",
			Size:     int(elem.Size),
		},
		Body: elem.Name,
	}

	converted := &ConvertedMessage{
		Intent:  intent,
		Type:    event.EventMessage,
		Content: content,
	}

	url := source.Client.GetGroupFileUrl(p.Key.UID.IntUin(), elem.Path, elem.Busid)
	data, mime, err := download(url)
	if err != nil {
		return p.makeMediaBridgeFailureMessage(msgKey, errors.New("failed to download group file from QQ"), converted)
	}

	content.Info.MimeType = mime.String()

	err = p.uploadMedia(intent, data, content)
	if err != nil {
		if errors.Is(err, mautrix.MTooLarge) {
			return p.makeMediaBridgeFailureMessage(msgKey, errors.New("homeserver rejected too large file"), converted)
		} else if httpErr, ok := err.(mautrix.HTTPError); ok && httpErr.IsStatus(413) {
			return p.makeMediaBridgeFailureMessage(msgKey, errors.New("proxy rejected too large file"), converted)
		} else {
			return p.makeMediaBridgeFailureMessage(msgKey, fmt.Errorf("failed to upload media: %w", err), converted)
		}
	}

	return converted
}

func (p *Portal) convertQQVoice(source *User, msgKey database.MessageKey, elem *message.VoiceElement, intent *appservice.IntentAPI) *ConvertedMessage {
	content := &event.MessageEventContent{
		MsgType: event.MsgAudio,
		Info: &event.FileInfo{
			MimeType: "audio/ogg",
			Size:     0,
		},
		Body: elem.Name,
	}

	converted := &ConvertedMessage{
		Intent:  intent,
		Type:    event.EventMessage,
		Content: content,
	}

	data, _, err := download(elem.Url)
	if err != nil {
		return p.makeMediaBridgeFailureMessage(msgKey, errors.New("failed to download group file from QQ"), converted)
	}

	oggData, err := convertToOgg(data)
	if err != nil {
		return p.makeMediaBridgeFailureMessage(msgKey, errors.New("failed to convert silk audio to ogg format"), converted)
	}

	content.Info.Size = len(oggData)

	err = p.uploadMedia(intent, oggData, content)
	if err != nil {
		if errors.Is(err, mautrix.MTooLarge) {
			return p.makeMediaBridgeFailureMessage(msgKey, errors.New("homeserver rejected too large file"), converted)
		} else if httpErr, ok := err.(mautrix.HTTPError); ok && httpErr.IsStatus(413) {
			return p.makeMediaBridgeFailureMessage(msgKey, errors.New("proxy rejected too large file"), converted)
		} else {
			return p.makeMediaBridgeFailureMessage(msgKey, fmt.Errorf("failed to upload media: %w", err), converted)
		}
	}

	return converted
}

func (p *Portal) convertQQLocation(source *User, msgKey database.MessageKey, elem *message.LightAppElement, intent *appservice.IntentAPI) *ConvertedMessage {
	name := gjson.Get(elem.Content, "meta.*.name").String()
	address := gjson.Get(elem.Content, "meta.*.address").String()
	latitude := gjson.Get(elem.Content, "meta.*.lat").Float()
	longitude := gjson.Get(elem.Content, "meta.*.lng").Float()

	url := fmt.Sprintf("https://maps.google.com/?q=%.5f,%.5f", latitude, longitude)

	content := &event.MessageEventContent{
		MsgType:       event.MsgLocation,
		Body:          fmt.Sprintf("Location: %s\n%s\n%s", name, address, url),
		Format:        event.FormatHTML,
		FormattedBody: fmt.Sprintf("Location: <a href='%s'>%s</a><br>%s", url, name, address),
		GeoURI:        fmt.Sprintf("geo:%.5f,%.5f", latitude, longitude),
	}

	return &ConvertedMessage{
		Intent:  intent,
		Type:    event.EventMessage,
		Content: content,
	}
}

func (p *Portal) convertQQImage(source *User, msgKey database.MessageKey, url string, intent *appservice.IntentAPI) *ConvertedMessage {
	content := &event.MessageEventContent{
		MsgType: event.MsgImage,
		Info: &event.FileInfo{
			MimeType: "application/octet-stream",
			Size:     0,
		},
		Body: "application/octet-stream",
	}

	converted := &ConvertedMessage{
		Intent:  intent,
		Type:    event.EventMessage,
		Content: content,
	}

	data, mime, err := download(url)
	if err != nil {
		return p.makeMediaBridgeFailureMessage(msgKey, errors.New("failed to download image from QQ"), converted)
	}

	content.Info.MimeType = mime.String()
	content.Info.Size = len(data)
	content.Body = mime.String()

	err = p.uploadMedia(intent, data, content)
	if err != nil {
		if errors.Is(err, mautrix.MTooLarge) {
			return p.makeMediaBridgeFailureMessage(msgKey, errors.New("homeserver rejected too large file"), converted)
		} else if httpErr, ok := err.(mautrix.HTTPError); ok && httpErr.IsStatus(413) {
			return p.makeMediaBridgeFailureMessage(msgKey, errors.New("proxy rejected too large file"), converted)
		} else {
			return p.makeMediaBridgeFailureMessage(msgKey, fmt.Errorf("failed to upload media: %w", err), converted)
		}
	}

	return converted
}

func (p *Portal) renderQQImage(url string, intent *appservice.IntentAPI) string {
	data, mime, err := download(url)
	if err != nil {
		p.log.Warnfln("failed to download image from QQ: %v", err)
		return "[图片]"
	}

	content := &event.MessageEventContent{
		MsgType: event.MsgImage,
		Info: &event.FileInfo{
			MimeType: mime.String(),
			Size:     len(data),
		},
		Body: mime.String(),
	}
	err = p.uploadMedia(intent, data, content)
	if err != nil {
		p.log.Warnfln("failed to upload media: %w", err)
		return "[图片]"
	}

	return fmt.Sprintf("![%s](%s)", mime.String(), content.URL)
}

func (p *Portal) renderQQLightApp(elem *message.LightAppElement) string {
	title := gjson.Get(elem.Content, "meta.*.title").String()
	desc := gjson.Get(elem.Content, "meta.*.desc").String()
	url := gjson.Get(elem.Content, "meta.*.qqdocurl").String()
	jumpUrl := gjson.Get(elem.Content, "meta.*.jumpUrl").String()
	if len(url) > 0 {
		return fmt.Sprintf("%s\n\nvia [%s](%s)", desc, title, url)
	}
	if len(jumpUrl) > 0 {
		tag := gjson.Get(elem.Content, "meta.*.tag").String()
		return fmt.Sprintf("**%s**\n\n%s\n\nvia [%s](%s)", title, desc, tag, jumpUrl)
	}

	return elem.Content
}

func (p *Portal) handleQQMessage(source *User, msg PortalMessage) {
	if len(p.MXID) == 0 {
		p.log.Warnln("handleQQMessage called even though portal.MXID is empty")
		return
	}

	var msgKey database.MessageKey
	var sender types.UID
	var elems []message.IMessageElement
	ts := time.Now().UnixMilli()

	switch {
	case msg.private != nil:
		msgKey = database.NewMessageKey(msg.private.Id, msg.private.InternalId)
		sender = types.NewIntUserUID(msg.private.Sender.Uin)
		elems = msg.private.Elements
		ts = int64(msg.private.Time) * 1000
	case msg.group != nil:
		msgKey = database.NewMessageKey(msg.group.Id, msg.group.InternalId)
		sender = types.NewIntUserUID(msg.group.Sender.Uin)
		elems = msg.group.Elements
		ts = int64(msg.group.Time) * 1000
	case msg.temp != nil:
		msgKey = database.NewPartialKey(int64(msg.temp.Id))
		sender = types.NewIntUserUID(msg.temp.Sender.Uin)
		elems = msg.temp.Elements
	case msg.offline != nil:
		msgKey = database.NewPartialKey(time.Now().Unix())
		sender = types.NewIntUserUID(msg.offline.Sender)
	}

	existingMsg := p.bridge.DB.Message.GetByMessageKey(p.Key, msgKey)
	if existingMsg != nil {
		p.log.Debugfln("Not handling %s: message is duplicate", msgKey)
		return
	}

	intent := p.getMessageIntent(source, sender)
	if intent == nil {
		return
	} else if !intent.IsCustomPuppet && p.IsPrivateChat() && sender.Uin == p.Key.Receiver.Uin {
		p.log.Debugfln("Not handling %s: user doesn't have double puppeting enabled", msgKey)
		return
	}

	var converted *ConvertedMessage
	var replyInfo *ReplyInfo

	var summary []string
	mentions := make(map[int]string)
	isRichFormat := false
	isSingleImage := false
	if msg.offline != nil {
		converted = p.convertQQFile(source, msgKey, msg.offline, intent)
	} else {
		for _, e := range elems {
			switch v := e.(type) {
			case *message.TextElement:
				summary = append(summary, v.Content)
			case *message.FaceElement:
				summary = append(summary, "/"+v.Name)
			case *message.AtElement:
				summary = append(summary, v.Display)
				if v.Target == 0 {
					mentions[len(summary)-1] = "@room"
				} else {
					mxid, name := p.bridge.Formatter.GetMatrixInfoByUID(p.MXID, types.NewIntUserUID(v.Target))
					mentions[len(summary)-1] = fmt.Sprintf(`<a href="https://matrix.to/#/%s">%s</a>`, mxid, name)
				}
			case *message.FriendImageElement:
				// rich format can't display gif properly...
				if len(elems) == 1 {
					converted = p.convertQQImage(source, msgKey, v.Url, intent)
				} else {
					summary = append(summary, p.renderQQImage(v.Url, intent))
					isRichFormat = true
				}
			case *message.GroupImageElement:
				// rich format can't display gif properly...
				if len(elems) == 1 {
					converted = p.convertQQImage(source, msgKey, v.Url, intent)
				} else {
					summary = append(summary, p.renderQQImage(v.Url, intent))
					isRichFormat = true
				}
			case *message.ShortVideoElement:
				converted = p.convertQQVideo(source, msgKey, v, intent)
			case *message.GroupFileElement:
				converted = p.convertQQGroupFile(source, msgKey, v, intent)
			case *message.VoiceElement:
				converted = p.convertQQVoice(source, msgKey, v, intent)
			case *message.ReplyElement:
				replyInfo = &ReplyInfo{
					ReplySeq: v.ReplySeq,
					Time:     v.Time,
					Sender:   types.NewIntUserUID(v.Sender),
				}
			case *message.LightAppElement:
				view := getAppView(v)
				if view == "LocationShare" {
					converted = p.convertQQLocation(source, msgKey, v, intent)
				} else {
					summary = append(summary, p.renderQQLightApp(v))
					isRichFormat = true
				}
			}
		}
	}

	if !isSingleImage && len(summary) > 0 {
		body := strings.Join(summary, "")
		content := &event.MessageEventContent{
			Body:    body,
			MsgType: event.MsgText,
		}
		if len(mentions) > 0 {
			offset := 0
			if replyInfo != nil {
				summary = summary[1:]
				content.Body = strings.Join(summary, "")
				offset = 1
			}
			var sb strings.Builder
			for pos, s := range summary {
				if val, ok := mentions[pos+offset]; ok {
					sb.WriteString(val)
				} else {
					sb.WriteString(s)
				}
			}

			content.Format = event.FormatHTML
			content.FormattedBody = sb.String()
		} else if isRichFormat {
			content.Format = event.FormatHTML
			content.FormattedBody = format.RenderMarkdown(body, true, false).FormattedBody
		}

		converted = &ConvertedMessage{
			Intent:  intent,
			Type:    event.EventMessage,
			Content: content,
			Extra:   map[string]interface{}{},
		}
	}

	if replyInfo != nil {
		p.SetReply(converted.Content, replyInfo)
	}

	var eventID id.EventID
	resp, err := p.sendMessage(converted.Intent, converted.Type, converted.Content, converted.Extra, ts)
	if err != nil {
		p.log.Errorfln("Failed to send %s to Matrix: %v", msgKey, err)
	} else {
		eventID = resp.EventID
	}

	if len(eventID) != 0 {
		p.finishHandling(existingMsg, msgKey, time.UnixMilli(ts), sender, eventID, database.MsgNormal, converted.Error, message.ToReadableString(elems))
	}
}

func (p *Portal) isRecentlyHandled(id string, error database.MessageErrorType) bool {
	start := p.recentlyHandledIndex
	lookingForMsg := recentlyHandledWrapper{id, error}
	for i := start; i != start; i = (i - 1) % recentlyHandledLength {
		if p.recentlyHandled[i] == lookingForMsg {
			return true
		}
	}

	return false
}

func (p *Portal) markHandled(txn dbutil.Transaction, msg *database.Message, msgKey database.MessageKey, ts time.Time, sender types.UID, mxid id.EventID, isSent, recent bool, msgType database.MessageType, errType database.MessageErrorType, content string) *database.Message {
	if msg == nil {
		msg = p.bridge.DB.Message.New()
		msg.Chat = p.Key
		msg.Key = msgKey
		msg.MXID = mxid
		msg.Timestamp = ts
		msg.Sender = sender
		msg.Sent = isSent
		msg.Type = msgType
		msg.Error = errType
		msg.Content = content
		msg.Insert(txn)
	} else {
		msg.UpdateMXID(txn, mxid, msgType, errType)
	}

	if recent {
		p.recentlyHandledLock.Lock()
		index := p.recentlyHandledIndex
		p.recentlyHandledIndex = (p.recentlyHandledIndex + 1) % recentlyHandledLength
		p.recentlyHandledLock.Unlock()
		p.recentlyHandled[index] = recentlyHandledWrapper{msg.Key.String(), errType}
	}

	return msg
}

func (p *Portal) getMessagePuppet(user *User, sender types.UID) *Puppet {
	puppet := p.bridge.GetPuppetByUID(sender)
	if puppet == nil {
		p.log.Warnfln("Message doesn't seem to have a valid sender (%s): puppet is nil", sender)
		return nil
	}

	user.EnqueuePortalResync(p)
	puppet.SyncContact(user, false, "handling message")

	return puppet
}

func (p *Portal) getMessageIntent(user *User, sender types.UID) *appservice.IntentAPI {
	puppet := p.getMessagePuppet(user, sender)
	if puppet == nil {
		return nil
	}

	return puppet.IntentFor(p)
}

func (p *Portal) finishHandling(existing *database.Message, msgKey database.MessageKey, ts time.Time, sender types.UID, mxid id.EventID, msgType database.MessageType, errType database.MessageErrorType, content string) {
	p.markHandled(nil, existing, msgKey, ts, sender, mxid, true, true, msgType, errType, content)
	p.log.Debugfln("Portal(%s) handled seq(%s) id(%s) %d %s -> %s ", p.Key, msgKey.Seq, msgKey.ID, ts.Unix(), msgType, mxid)
}

func (p *Portal) kickExtraUsers(participantMap map[types.UID]bool) {
	members, err := p.MainIntent().JoinedMembers(p.MXID)
	if err != nil {
		p.log.Warnln("Failed to get member list:", err)
		return
	}
	for member := range members.Joined {
		uid, ok := p.bridge.ParsePuppetMXID(member)
		if ok {
			_, shouldBePresent := participantMap[uid]
			if !shouldBePresent {
				_, err = p.MainIntent().KickUser(p.MXID, &mautrix.ReqKickUser{
					UserID: member,
					Reason: "User had left this QQ chat",
				})
				if err != nil {
					p.log.Warnfln("Failed to kick user %s who had left: %v", member, err)
				}
			}
		}
	}
}

func (p *Portal) syncParticipant(source *User, participant *client.GroupMemberInfo, puppet *Puppet, user *User, forceAvatarSync bool, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
		if err := recover(); err != nil {
			p.log.Errorfln("Syncing participant %s panicked: %v\n%s", participant.Uin, err, debug.Stack())
		}
	}()

	puppet.SyncContact(source, forceAvatarSync, "group participant")
	p.UpdateRoomNickname(participant)
	if user != nil && user != source {
		p.ensureUserInvited(user)
	}
	if user == nil || !puppet.IntentFor(p).IsCustomPuppet {
		err := puppet.IntentFor(p).EnsureJoined(p.MXID)
		if err != nil {
			p.log.Warnfln("Failed to make puppet of %s join %s: %v", participant.Uin, p.MXID, err)
		}
	}
}

func (p *Portal) SyncParticipants(source *User, metadata *client.GroupInfo, forceAvatarSync bool) {
	changed := false
	levels, err := p.MainIntent().PowerLevels(p.MXID)
	if err != nil {
		levels = p.GetBasePowerLevels()
		changed = true
	}

	changed = p.applyPowerLevelFixes(levels) || changed
	var wg sync.WaitGroup
	wg.Add(len(metadata.Members))
	participantMap := make(map[types.UID]bool)
	for _, participant := range metadata.Members {
		uid := types.NewIntUserUID(participant.Uin)
		participantMap[uid] = true
		puppet := p.bridge.GetPuppetByUID(uid)
		user := p.bridge.GetUserByUID(uid)

		if p.bridge.Config.Bridge.ParallelMemberSync {
			go p.syncParticipant(source, participant, puppet, user, forceAvatarSync, &wg)
		} else {
			p.syncParticipant(source, participant, puppet, user, forceAvatarSync, &wg)
		}

		expectedLevel := 0
		if participant.Permission == client.Owner {
			expectedLevel = 95
		} else if participant.Permission == client.Administrator {
			expectedLevel = 50
		}
		changed = levels.EnsureUserLevel(puppet.MXID, expectedLevel) || changed
		if user != nil {
			changed = levels.EnsureUserLevel(user.MXID, expectedLevel) || changed
		}
	}

	if changed {
		_, err = p.MainIntent().SetPowerLevels(p.MXID, levels)
		if err != nil {
			p.log.Errorln("Failed to change power levels:", err)
		}
	}

	p.kickExtraUsers(participantMap)
	wg.Wait()
	p.log.Debugln("Participant sync completed")
}

func (p *Portal) UpdateRoomNickname(info *client.GroupMemberInfo) {
	if len(info.CardName) <= 0 {
		return
	}
	puppet := p.bridge.GetPuppetByUID(types.NewIntUserUID(info.Uin))

	roomNickname, _ := p.bridge.Config.Bridge.FormatDisplayname(*types.NewContact(
		info.Uin, info.CardName, "",
	))
	memberContent := puppet.IntentFor(p).Member(p.MXID, puppet.MXID)
	if memberContent.Displayname != roomNickname {
		memberContent.Displayname = roomNickname
		if _, err := puppet.DefaultIntent().SendStateEvent(
			p.MXID, event.StateMember, puppet.MXID.String(), memberContent); err == nil {
			p.bridge.AS.StateStore.SetMember(p.MXID, puppet.MXID, memberContent)
		}
	}
}

func (p *Portal) UpdateAvatar(user *User, setBy types.UID, updateInfo bool) bool {
	p.avatarLock.Lock()
	defer p.avatarLock.Unlock()

	changed := user.updateAvatar(p.Key.UID, &p.Avatar, &p.AvatarURL, &p.AvatarSet, p.log, p.MainIntent())
	if !changed || p.Avatar == "unauthorized" {
		if changed || updateInfo {
			p.Update(nil)
		}
		return changed
	}

	if len(p.MXID) > 0 {
		intent := p.MainIntent()
		if !setBy.IsEmpty() {
			intent = p.bridge.GetPuppetByUID(setBy).IntentFor(p)
		}
		_, err := intent.SetRoomAvatar(p.MXID, p.AvatarURL)
		if errors.Is(err, mautrix.MForbidden) && intent != p.MainIntent() {
			_, err = p.MainIntent().SetRoomAvatar(p.MXID, p.AvatarURL)
		}
		if err != nil {
			p.log.Warnln("Failed to set room avatar:", err)
			return true
		} else {
			p.AvatarSet = true
		}
	}

	if updateInfo {
		p.UpdateBridgeInfo()
		p.Update(nil)
	}

	return true
}

func (p *Portal) UpdateName(name string, setBy types.UID, updateInfo bool) bool {
	if p.Name != name || (!p.NameSet && len(p.MXID) > 0) {
		p.log.Debugfln("Updating name %q -> %q", p.Name, name)
		p.Name = name
		p.NameSet = false
		if updateInfo {
			defer p.Update(nil)
		}

		if len(p.MXID) > 0 {
			intent := p.MainIntent()
			if !setBy.IsEmpty() {
				intent = p.bridge.GetPuppetByUID(setBy).IntentFor(p)
			}
			_, err := intent.SetRoomName(p.MXID, name)
			if errors.Is(err, mautrix.MForbidden) && intent != p.MainIntent() {
				_, err = p.MainIntent().SetRoomName(p.MXID, name)
			}
			if err == nil {
				p.NameSet = true
				if updateInfo {
					p.UpdateBridgeInfo()
				}

				return true
			} else {
				p.log.Warnln("Failed to set room name:", err)
			}
		}
	}

	return false
}

func (p *Portal) UpdateTopic(topic string, setBy types.UID, updateInfo bool) bool {
	if p.Topic != topic || !p.TopicSet {
		p.log.Debugfln("Updating topic %q -> %q", p.Topic, topic)
		p.Topic = topic
		p.TopicSet = false

		intent := p.MainIntent()
		if !setBy.IsEmpty() {
			intent = p.bridge.GetPuppetByUID(setBy).IntentFor(p)
		}
		_, err := intent.SetRoomTopic(p.MXID, topic)
		if errors.Is(err, mautrix.MForbidden) && intent != p.MainIntent() {
			_, err = p.MainIntent().SetRoomTopic(p.MXID, topic)
		}
		if err == nil {
			p.TopicSet = true
			if updateInfo {
				p.UpdateBridgeInfo()
				p.Update(nil)
			}

			return true
		} else {
			p.log.Warnln("Failed to set room topic:", err)
		}
	}

	return false
}

func (p *Portal) UpdateMetadata(user *User, groupInfo *client.GroupInfo, forceAvatarSync bool) bool {
	if p.IsPrivateChat() {
		return false
	}

	if groupInfo == nil {
		p.log.Errorln("Failed to get group info", groupInfo.Code)
		return false
	}

	p.SyncParticipants(user, groupInfo, forceAvatarSync)

	update := false
	update = p.UpdateName(groupInfo.Name, types.EmptyUID, false) || update

	groups, err := user.Client.SearchGroupByKeyword(strconv.FormatInt(groupInfo.Code, 10))
	if err != nil {
		p.log.Warnln("Failed to search group", groupInfo.Code)
	} else {
		for _, group := range groups {
			if group.Code == groupInfo.Code {
				update = p.UpdateTopic(group.Memo, types.EmptyUID, false) || update
				break
			}
		}
	}

	// TODO: restrict message sending and changes

	return update
}

func (p *Portal) ensureUserInvited(user *User) bool {
	return user.ensureInvited(p.MainIntent(), p.MXID, p.IsPrivateChat())
}

func (p *Portal) UpdateMatrixRoom(user *User, groupInfo *client.GroupInfo, forceAvatarSync bool) bool {
	if len(p.MXID) == 0 {
		return false
	}
	p.log.Infofln("Syncing portal %s for %s", p.Key, user.MXID)

	p.ensureUserInvited(user)
	go p.addToSpace(user)

	update := false
	update = p.UpdateMetadata(user, groupInfo, forceAvatarSync) || update
	if !p.IsPrivateChat() {
		update = p.UpdateAvatar(user, types.EmptyUID, false) || update
	}
	if update || p.LastSync.Add(24*time.Hour).Before(time.Now()) {
		p.LastSync = time.Now()
		p.Update(nil)
		p.UpdateBridgeInfo()
	}

	return true
}

func (p *Portal) GetBasePowerLevels() *event.PowerLevelsEventContent {
	anyone := 0
	nope := 99
	invite := 50
	if p.bridge.Config.Bridge.AllowUserInvite {
		invite = 0
	}
	return &event.PowerLevelsEventContent{
		UsersDefault:    anyone,
		EventsDefault:   anyone,
		RedactPtr:       &anyone,
		StateDefaultPtr: &nope,
		BanPtr:          &nope,
		InvitePtr:       &invite,
		Users: map[id.UserID]int{
			p.MainIntent().UserID: 100,
		},
		Events: map[string]int{
			event.StateRoomName.Type:   anyone,
			event.StateRoomAvatar.Type: anyone,
			event.StateTopic.Type:      anyone,
			event.EventReaction.Type:   anyone,
			event.EventRedaction.Type:  anyone,
		},
	}
}

func (p *Portal) applyPowerLevelFixes(levels *event.PowerLevelsEventContent) bool {
	changed := false
	changed = levels.EnsureEventLevel(event.EventReaction, 0) || changed
	changed = levels.EnsureEventLevel(event.EventRedaction, 0) || changed

	return changed
}

func (p *Portal) ChangeAdminStatus(uids []types.UID, setAdmin bool) id.EventID {
	levels, err := p.MainIntent().PowerLevels(p.MXID)
	if err != nil {
		levels = p.GetBasePowerLevels()
	}
	newLevel := 0
	if setAdmin {
		newLevel = 50
	}

	changed := p.applyPowerLevelFixes(levels)
	for _, uid := range uids {
		puppet := p.bridge.GetPuppetByUID(uid)
		changed = levels.EnsureUserLevel(puppet.MXID, newLevel) || changed

		user := p.bridge.GetUserByUID(uid)
		if user != nil {
			changed = levels.EnsureUserLevel(user.MXID, newLevel) || changed
		}
	}

	if changed {
		resp, err := p.MainIntent().SetPowerLevels(p.MXID, levels)
		if err != nil {
			p.log.Errorln("Failed to change power levels:", err)
		} else {
			return resp.EventID
		}
	}

	return ""
}

func (p *Portal) RestrictMessageSending(restrict bool) id.EventID {
	levels, err := p.MainIntent().PowerLevels(p.MXID)
	if err != nil {
		levels = p.GetBasePowerLevels()
	}

	newLevel := 0
	if restrict {
		newLevel = 50
	}

	changed := p.applyPowerLevelFixes(levels)
	if levels.EventsDefault == newLevel && !changed {
		return ""
	}

	levels.EventsDefault = newLevel
	resp, err := p.MainIntent().SetPowerLevels(p.MXID, levels)
	if err != nil {
		p.log.Errorln("Failed to change power levels:", err)
		return ""
	} else {
		return resp.EventID
	}
}

func (p *Portal) RestrictMetadataChanges(restrict bool) id.EventID {
	levels, err := p.MainIntent().PowerLevels(p.MXID)
	if err != nil {
		levels = p.GetBasePowerLevels()
	}
	newLevel := 0
	if restrict {
		newLevel = 50
	}

	changed := p.applyPowerLevelFixes(levels)
	changed = levels.EnsureEventLevel(event.StateRoomName, newLevel) || changed
	changed = levels.EnsureEventLevel(event.StateRoomAvatar, newLevel) || changed
	changed = levels.EnsureEventLevel(event.StateTopic, newLevel) || changed
	if changed {
		resp, err := p.MainIntent().SetPowerLevels(p.MXID, levels)
		if err != nil {
			p.log.Errorln("Failed to change power levels:", err)
		} else {
			return resp.EventID
		}
	}

	return ""
}

func (p *Portal) getBridgeInfoStateKey() string {
	return fmt.Sprintf("me.lxduo.qq://qq/%s", p.Key.UID)
}

func (p *Portal) getBridgeInfo() (string, event.BridgeEventContent) {
	bridgeInfo := event.BridgeEventContent{
		BridgeBot: p.bridge.Bot.UserID,
		Creator:   p.MainIntent().UserID,
		Protocol: event.BridgeInfoSection{
			ID:          "qq",
			DisplayName: "QQ",
			AvatarURL:   p.bridge.Config.AppService.Bot.ParsedAvatar.CUString(),
			ExternalURL: "https://www.qq.com/",
		},
		Channel: event.BridgeInfoSection{
			ID:          p.Key.UID.String(),
			DisplayName: p.Name,
			AvatarURL:   p.AvatarURL.CUString(),
		},
	}

	return p.getBridgeInfoStateKey(), bridgeInfo
}

func (p *Portal) UpdateBridgeInfo() {
	if len(p.MXID) == 0 {
		p.log.Debugln("Not updating bridge info: no Matrix room created")
		return
	}
	p.log.Debugln("Updating bridge info...")
	stateKey, content := p.getBridgeInfo()
	_, err := p.MainIntent().SendStateEvent(p.MXID, event.StateBridge, stateKey, content)
	if err != nil {
		p.log.Warnln("Failed to update m.bridge:", err)
	}
	// TODO remove this once https://github.com/matrix-org/matrix-doc/pull/2346 is in spec
	_, err = p.MainIntent().SendStateEvent(p.MXID, event.StateHalfShotBridge, stateKey, content)
	if err != nil {
		p.log.Warnln("Failed to update uk.half-shot.bridge:", err)
	}
}

func (p *Portal) GetEncryptionEventContent() (evt *event.EncryptionEventContent) {
	evt = &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1}
	if rot := p.bridge.Config.Bridge.Encryption.Rotation; rot.EnableCustom {
		evt.RotationPeriodMillis = rot.Milliseconds
		evt.RotationPeriodMessages = rot.Messages
	}
	return
}

func (p *Portal) CreateMatrixRoom(user *User, groupInfo *client.GroupInfo, isFullInfo bool) error {
	if len(p.MXID) > 0 {
		return nil
	}

	p.roomCreateLock.Lock()
	defer p.roomCreateLock.Unlock()

	intent := p.MainIntent()
	if err := intent.EnsureRegistered(); err != nil {
		return err
	}

	p.log.Infoln("Creating Matrix room. Info source:", user.MXID)

	if p.IsPrivateChat() {
		puppet := p.bridge.GetPuppetByUID(p.Key.UID)
		puppet.SyncContact(user, true, "creating private chat portal")
		if p.bridge.Config.Bridge.PrivateChatPortalMeta {
			p.Name = puppet.Displayname
			p.AvatarURL = puppet.AvatarURL
			p.Avatar = puppet.Avatar
		} else {
			p.Name = ""
		}
		p.Topic = PrivateChatTopic
	} else {
		if groupInfo == nil || !isFullInfo {
			foundInfo := user.Client.FindGroup(p.Key.UID.IntUin())
			if foundInfo == nil {
				p.log.Warnfln("Failed to get group info through %s", user.UID)
			} else {
				m, err := user.Client.GetGroupMembers(foundInfo)
				if err != nil {
					p.log.Warnfln("Failed to get group members through %s: %v", user.UID, err)
				} else {
					foundInfo.Members = m
					groupInfo = foundInfo
					isFullInfo = true
				}
			}
		}
		if groupInfo != nil {
			p.Name = groupInfo.Name
			//p.Topic = groupInfo.Topic
		}
		p.UpdateAvatar(user, types.EmptyUID, false)
	}

	bridgeInfoStateKey, bridgeInfo := p.getBridgeInfo()

	initialState := []*event.Event{{
		Type: event.StatePowerLevels,
		Content: event.Content{
			Parsed: p.GetBasePowerLevels(),
		},
	}, {
		Type:     event.StateBridge,
		Content:  event.Content{Parsed: bridgeInfo},
		StateKey: &bridgeInfoStateKey,
	}, {
		// TODO remove this once https://github.com/matrix-org/matrix-doc/pull/2346 is in spec
		Type:     event.StateHalfShotBridge,
		Content:  event.Content{Parsed: bridgeInfo},
		StateKey: &bridgeInfoStateKey,
	}}
	if !p.AvatarURL.IsEmpty() {
		initialState = append(initialState, &event.Event{
			Type: event.StateRoomAvatar,
			Content: event.Content{
				Parsed: event.RoomAvatarEventContent{URL: p.AvatarURL},
			},
		})
		p.AvatarSet = true
	}

	var invite []id.UserID

	if p.bridge.Config.Bridge.Encryption.Default {
		initialState = append(initialState, &event.Event{
			Type: event.StateEncryption,
			Content: event.Content{
				Parsed: p.GetEncryptionEventContent(),
			},
		})
		p.Encrypted = true
		if p.IsPrivateChat() {
			invite = append(invite, p.bridge.Bot.UserID)
		}
	}

	creationContent := make(map[string]interface{})
	if !p.bridge.Config.Bridge.FederateRooms {
		creationContent["m.federate"] = false
	}
	resp, err := intent.CreateRoom(&mautrix.ReqCreateRoom{
		Visibility:      "private",
		Name:            p.Name,
		Topic:           p.Topic,
		Invite:          invite,
		Preset:          "private_chat",
		IsDirect:        p.IsPrivateChat(),
		InitialState:    initialState,
		CreationContent: creationContent,
	})
	if err != nil {
		return err
	}
	p.NameSet = len(p.Name) > 0
	p.TopicSet = len(p.Topic) > 0
	p.MXID = resp.RoomID
	p.bridge.portalsLock.Lock()
	p.bridge.portalsByMXID[p.MXID] = p
	p.bridge.portalsLock.Unlock()
	p.Update(nil)
	p.log.Infoln("Matrix room created:", p.MXID)

	for _, userID := range invite {
		p.bridge.StateStore.SetMembership(p.MXID, userID, event.MembershipInvite)
	}

	p.ensureUserInvited(user)
	// TODO: sync chat double puppet detail

	go p.addToSpace(user)

	if groupInfo != nil {
		p.SyncParticipants(user, groupInfo, true)
		// TODO: restrict message sending and changes
	}
	if p.IsPrivateChat() {
		puppet := user.bridge.GetPuppetByUID(p.Key.UID)

		if p.bridge.Config.Bridge.Encryption.Default {
			err = p.bridge.Bot.EnsureJoined(p.MXID)
			if err != nil {
				p.log.Errorln("Failed to join created portal with bridge bot for e2be:", err)
			}
		}

		user.UpdateDirectChats(map[id.UserID][]id.RoomID{puppet.MXID: {p.MXID}})
	}

	firstEventResp, err := p.MainIntent().SendMessageEvent(p.MXID, PortalCreationDummyEvent, struct{}{})
	if err != nil {
		p.log.Errorln("Failed to send dummy event to mark portal creation:", err)
	} else {
		p.FirstEventID = firstEventResp.EventID
		p.Update(nil)
	}

	return nil
}

func (p *Portal) addToSpace(user *User) {
	spaceID := user.GetSpaceRoom()
	if len(spaceID) == 0 || user.IsInSpace(p.Key) {
		return
	}
	_, err := p.bridge.Bot.SendStateEvent(spaceID, event.StateSpaceChild, p.MXID.String(), &event.SpaceChildEventContent{
		Via: []string{p.bridge.Config.Homeserver.Domain},
	})
	if err != nil {
		p.log.Errorfln("Failed to add room to %s's personal filtering space (%s): %v", user.MXID, spaceID, err)
	} else {
		p.log.Debugfln("Added room to %s's personal filtering space (%s)", user.MXID, spaceID)
		user.MarkInSpace(p.Key)
	}
}

func (p *Portal) IsPrivateChat() bool {
	return p.Key.UID.Type == types.User
}

func (p *Portal) IsGroupChat() bool {
	return p.Key.UID.Type == types.Group
}

func (p *Portal) MainIntent() *appservice.IntentAPI {
	if p.IsPrivateChat() {
		return p.bridge.GetPuppetByUID(p.Key.UID).DefaultIntent()
	}

	return p.bridge.Bot
}

func (p *Portal) SetReply(content *event.MessageEventContent, replyTo *ReplyInfo) bool {
	if replyTo == nil {
		return false
	}
	msgSeq := strconv.FormatInt(int64(replyTo.ReplySeq), 10)
	message := p.bridge.DB.Message.GetByReply(p.Key, msgSeq, int64(replyTo.Time))
	p.log.Debugfln("Portal(%s) query reply seq(%d) %d", p.Key, msgSeq, replyTo.Time)
	if message == nil || message.IsFakeMXID() {
		return false
	}
	evt, err := p.MainIntent().GetEvent(p.MXID, message.MXID)
	if err != nil {
		p.log.Warnln("Failed to get reply target:", err)
		content.RelatesTo = (&event.RelatesTo{}).SetReplyTo(message.MXID)
		return true
	}
	_ = evt.Content.ParseRaw(evt.Type)
	if evt.Type == event.EventEncrypted {
		decryptedEvt, err := p.bridge.Crypto.Decrypt(evt)
		if err != nil {
			p.log.Warnln("Failed to decrypt reply target:", err)
		} else {
			evt = decryptedEvt
		}
	}
	content.SetReply(evt)

	return true
}

func (p *Portal) encrypt(intent *appservice.IntentAPI, content *event.Content, eventType event.Type) (event.Type, error) {
	if !p.Encrypted || p.bridge.Crypto == nil {
		return eventType, nil
	}
	intent.AddDoublePuppetValue(content)

	// TODO maybe the locking should be inside mautrix-go?
	p.encryptLock.Lock()
	defer p.encryptLock.Unlock()

	err := p.bridge.Crypto.Encrypt(p.MXID, eventType, content)
	if err != nil {
		return eventType, fmt.Errorf("failed to encrypt event: %w", err)
	}

	return event.EventEncrypted, nil
}

func (p *Portal) sendMessage(intent *appservice.IntentAPI, eventType event.Type, content *event.MessageEventContent, extraContent map[string]interface{}, timestamp int64) (*mautrix.RespSendEvent, error) {
	wrappedContent := event.Content{Parsed: content, Raw: extraContent}
	var err error
	eventType, err = p.encrypt(intent, &wrappedContent, eventType)
	if err != nil {
		return nil, err
	}

	_, _ = intent.UserTyping(p.MXID, false, 0)
	if timestamp == 0 {
		return intent.SendMessageEvent(p.MXID, eventType, &wrappedContent)
	} else {
		return intent.SendMassagedMessageEvent(p.MXID, eventType, &wrappedContent, timestamp)
	}
}

func (p *Portal) tryKickUser(userID id.UserID, intent *appservice.IntentAPI) error {
	_, err := intent.KickUser(p.MXID, &mautrix.ReqKickUser{UserID: userID})
	if err != nil {
		httpErr, ok := err.(mautrix.HTTPError)
		if ok && httpErr.RespError != nil && httpErr.RespError.ErrCode == "M_FORBIDDEN" {
			_, err = p.MainIntent().KickUser(p.MXID, &mautrix.ReqKickUser{UserID: userID})
		}
	}

	return err
}

func (p *Portal) removeUser(isSameUser bool, kicker *appservice.IntentAPI, target id.UserID, targetIntent *appservice.IntentAPI) {
	if !isSameUser || targetIntent == nil {
		err := p.tryKickUser(target, kicker)
		if err != nil {
			p.log.Warnfln("Failed to kick %s from %s: %v", target, p.MXID, err)
			if targetIntent != nil {
				_, _ = targetIntent.LeaveRoom(p.MXID)
			}
		}
	} else {
		_, err := targetIntent.LeaveRoom(p.MXID)
		if err != nil {
			p.log.Warnfln("Failed to leave portal as %s: %v", target, err)
			_, _ = p.MainIntent().KickUser(p.MXID, &mautrix.ReqKickUser{UserID: target})
		}
	}
	p.CleanupIfEmpty()
}

func (p *Portal) HandleQQGroupMemberInvite(source *User, senderUID types.UID, targetUID types.UID) {
	intent := p.MainIntent()
	if !senderUID.IsEmpty() {
		sender := p.bridge.GetPuppetByUID(senderUID)
		intent = sender.IntentFor(p)
	}

	puppet := p.bridge.GetPuppetByUID(targetUID)
	puppet.SyncContact(source, true, "handling QQ invite")
	_, err := intent.SendStateEvent(p.MXID, event.StateMember, puppet.MXID.String(), &event.MemberEventContent{
		Membership:  event.MembershipInvite,
		Displayname: puppet.Displayname,
		AvatarURL:   puppet.AvatarURL.CUString(),
	})
	if err != nil {
		p.log.Warnfln("Failed to invite %s as %s: %v", puppet.MXID, intent.UserID, err)
		_ = p.MainIntent().EnsureInvited(p.MXID, puppet.MXID)
	}
	err = puppet.DefaultIntent().EnsureJoined(p.MXID)
	if err != nil {
		p.log.Errorfln("Failed to ensure %s is joined: %v", puppet.MXID, err)
	}
}

func (p *Portal) HandleQQGroupMemberKick(source *User, senderUID types.UID, targetUID types.UID) {
	puppet := p.bridge.GetPuppetByUID(targetUID)
	if senderUID.IsEmpty() {
		p.removeUser(true, nil, puppet.MXID, puppet.DefaultIntent())
	} else {
		sender := p.bridge.GetPuppetByUID(senderUID)
		senderIntent := sender.IntentFor(p)
		p.removeUser(false, senderIntent, puppet.MXID, puppet.DefaultIntent())
	}
}

func (p *Portal) HandleQQMessageRevoke(source *User, msgSeq int32, ts int64, operator int64) {
	msg := p.bridge.DB.Message.GetByReply(p.Key, strconv.FormatInt(int64(msgSeq), 10), ts)
	if msg == nil || msg.IsFakeMsgID() {
		return
	}

	intent := p.bridge.GetPuppetByUID(types.NewIntUserUID(operator)).IntentFor(p)
	_, err := intent.RedactEvent(p.MXID, msg.MXID)
	if err != nil {
		if errors.Is(err, mautrix.MForbidden) {
			_, err = p.MainIntent().RedactEvent(p.MXID, msg.MXID)
			if err != nil {
				p.log.Errorln("Failed to redact %s: %v", msg.Key, err)
			}
		}
		//} else {
		//msg.Delete()
	}
}

func (p *Portal) makeMediaBridgeFailureMessage(msgKey database.MessageKey, bridgeErr error, converted *ConvertedMessage) *ConvertedMessage {
	p.log.Errorfln("Failed to bridge media for %s: %v", msgKey, bridgeErr)
	converted.Type = event.EventMessage
	converted.Content = &event.MessageEventContent{
		MsgType: event.MsgNotice,
		Body:    fmt.Sprintf("Failed to bridge media: %v", bridgeErr),
	}

	return converted
}

func (p *Portal) encryptFileInPlace(data []byte, mimeType string) (string, *event.EncryptedFileInfo) {
	if !p.Encrypted {
		return mimeType, nil
	}

	file := &event.EncryptedFileInfo{
		EncryptedFile: *attachment.NewEncryptedFile(),
		URL:           "",
	}
	file.EncryptInPlace(data)
	return "application/octet-stream", file
}

func (p *Portal) uploadMedia(intent *appservice.IntentAPI, data []byte, content *event.MessageEventContent) error {
	uploadMimeType, file := p.encryptFileInPlace(data, content.Info.MimeType)

	req := mautrix.ReqUploadMedia{
		ContentBytes: data,
		ContentType:  uploadMimeType,
	}
	var mxc id.ContentURI
	if p.bridge.Config.Homeserver.AsyncMedia {
		uploaded, err := intent.UnstableUploadAsync(req)
		if err != nil {
			return err
		}
		mxc = uploaded.ContentURI
	} else {
		uploaded, err := intent.UploadMedia(req)
		if err != nil {
			return err
		}
		mxc = uploaded.ContentURI
	}

	if file != nil {
		file.URL = mxc.CUString()
		content.File = file
	} else {
		content.URL = mxc.CUString()
	}

	content.Info.Size = len(data)
	if content.Info.Width == 0 && content.Info.Height == 0 && strings.HasPrefix(content.Info.MimeType, "image/") {
		cfg, _, _ := image.DecodeConfig(bytes.NewReader(data))
		content.Info.Width, content.Info.Height = cfg.Width, cfg.Height
	}

	return nil
}

func (p *Portal) preprocessMatrixMedia(content *event.MessageEventContent) (string, []byte, error) {
	fileName := content.Body
	if content.FileName != "" && content.Body != content.FileName {
		fileName = content.FileName
	}

	var file *event.EncryptedFileInfo
	rawMXC := content.URL
	if content.File != nil {
		file = content.File
		rawMXC = file.URL
	}
	mxc, err := rawMXC.Parse()
	if err != nil {
		return fileName, nil, err
	}
	data, err := p.MainIntent().DownloadBytesContext(context.Background(), mxc)
	if err != nil {
		return fileName, nil, util.NewDualError(errMediaDownloadFailed, err)
	}
	if file != nil {
		err = file.DecryptInPlace(data)
		if err != nil {
			return fileName, nil, util.NewDualError(errMediaDecryptFailed, err)
		}
	}

	return fileName, data, nil
}

func (p *Portal) renderQQMention(sender *User, uin int64) *message.AtElement {
	if p.Key.UID.IsGroup() {
		if info, err := sender.Client.GetMemberInfo(p.Key.UID.IntUin(), uin); err == nil {
			return message.NewAt(uin, "@"+info.DisplayName())
		}
	} else {
		if info := sender.Client.FindFriend(uin); info != nil {
			return message.NewAt(uin, "@"+info.Nickname)
		} else if info, err := sender.Client.GetSummaryInfo(uin); err == nil {
			return message.NewAt(uin, "@"+info.Nickname)
		}
	}

	return message.NewAt(uin, "@"+strconv.FormatInt(uin, 10))
}

func (p *Portal) renderQQLocation(latitude, longitude float64) *message.LightAppElement {
	locationJson := fmt.Sprintf(`
	{
		"app": "com.tencent.map",
		"desc": "地图",
		"view": "LocationShare",
		"ver": "0.0.0.1",
		"prompt": "[应用]地图",
		"from": 1,
		"meta": {
		  "Location.Search": {
			"id": "12250896297164027526",
			"name": "Location Share",
			"address": "Latitude: %.5f Longitude: %.5f",
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
	`, latitude, longitude, latitude, longitude)

	return message.NewLightApp(locationJson)
}

func (p *Portal) HandleMatrixMessage(sender *User, evt *event.Event) {
	if err := p.canBridgeFrom(sender); err != nil {
		return
	}

	var info message.Source
	target := p.Key.UID.IntUin()
	if p.Key.UID.IsGroup() {
		info = message.Source{
			SourceType: message.SourceGroup,
			PrimaryID:  target,
		}
	} else {
		info = message.Source{
			SourceType: message.SourcePrivate,
			PrimaryID:  target,
		}
	}

	content, ok := evt.Content.Parsed.(*event.MessageEventContent)
	if !ok {
		p.log.Warnfln("Failed to parse matrix message content")
		return
	}

	var elems []message.IMessageElement
	var reply *message.ReplyElement

	replyToID := content.GetReplyTo()
	var replyMention *message.AtElement
	if len(replyToID) > 0 {
		replyToMsg := p.bridge.DB.Message.GetByMXID(replyToID)
		if replyToMsg != nil && !replyToMsg.IsFakeMsgID() && replyToMsg.Type == database.MsgNormal {
			replySeq, err := strconv.ParseInt(replyToMsg.Key.Seq, 10, 32)
			if err != nil {
				replyMention = p.renderQQMention(sender, replyToMsg.Sender.IntUin())
			} else {
				var seq int32
				var groupID int64
				if p.Key.UID.IsUser() {
					seq = int32(uint16(int32(replySeq)))
					if replyToMsg.Sender.IntUin() == sender.Client.Uin {
						groupID = p.Key.UID.IntUin()
					} else {
						groupID = sender.Client.Uin
					}
				} else {
					seq = int32(replySeq)
					groupID = p.Key.UID.IntUin()
				}
				reply = &message.ReplyElement{
					ReplySeq: seq,
					GroupID:  groupID,
					Sender:   replyToMsg.Sender.IntUin(),
					Time:     int32(replyToMsg.Timestamp.Unix()),
					Elements: []message.IMessageElement{message.NewText(replyToMsg.Content)},
				}
			}
		}
	}

	if evt.Type == event.EventSticker {
		content.MsgType = event.MsgImage
	}

	switch content.MsgType {
	case event.MsgText, event.MsgEmote:
		if replyMention != nil {
			elems = []message.IMessageElement{replyMention}
		}

		if content.MsgType == event.MsgEmote {
			elems = append(elems, message.NewText("/me "))
		}

		// TODO: multi images
		text := content.Body
		if content.Format == event.FormatHTML {
			formatted := p.bridge.Formatter.ParseMatrix(content.FormattedBody)
			// fetch mention display name (QQ side)
			for _, elem := range formatted {
				if e, ok := elem.(*message.AtElement); ok {
					e.Display = p.renderQQMention(sender, e.Target).Display
				}
			}
			elems = append(elems, formatted...)
		} else {
			elems = append(elems, message.NewText(text))
		}
	case event.MsgImage:
		_, data, err := p.preprocessMatrixMedia(content)
		if data == nil {
			p.log.Warnfln("Failed to process matrix media: %v", err)
			return
		}
		e, err := sender.Client.UploadImage(info, bytes.NewReader(data), 4)
		if err != nil {
			p.log.Warnfln("Failed to upload image to QQ: %v", err)
			return
		}
		elems = []message.IMessageElement{e}
	case event.MsgVideo:
		_, data, err := p.preprocessMatrixMedia(content)
		if data == nil {
			p.log.Warnfln("Failed to process matrix media: %v", err)
			return
		}
		e, err := sender.Client.UploadShortVideo(info, bytes.NewReader(data), bytes.NewReader(smallestImg), 4)
		if err != nil {
			p.log.Warnfln("Failed to upload video to QQ: %v", err)
			return
		}
		elems = []message.IMessageElement{e}
	case event.MsgAudio:
		_, data, err := p.preprocessMatrixMedia(content)
		if data == nil {
			p.log.Warnfln("Failed to process matrix media: %v", err)
			return
		}
		silkData, err := convertToSilk(data)
		if err != nil {
			p.log.Warnfln("Failed to convert ogg audio to silk format: %s %v", silkData, err)
			return
		}
		e, err := sender.Client.UploadVoice(info, bytes.NewReader(silkData))
		if err != nil {
			p.log.Warnfln("Failed to upload voice to QQ: %v", err)
			return
		}
		elems = []message.IMessageElement{e}
	case event.MsgFile:
		fileName, data, err := p.preprocessMatrixMedia(content)
		if data == nil {
			p.log.Warnfln("Failed to process matrix media: %v", err)
			return
		}
		f := &client.LocalFile{
			FileName:     fileName,
			Body:         bytes.NewReader(data),
			RemoteFolder: "/",
		}
		if err := sender.Client.UploadFile(info, f); err != nil {
			p.log.Warnfln("Failed to upload file to QQ: %v", err)
			return
		}
	case event.MsgLocation:
		latitude, longitude, err := parseGeoURI(content.GeoURI)
		if err != nil {
			p.log.Warnfln("Failed to parse geo uri: %v", err)

		}
		elems = append(elems, p.renderQQLocation(latitude, longitude))
	default:
		p.log.Warnfln("%s not support", content.MsgType)
		return
	}

	if reply != nil {
		elems = append(elems, reply)
	}

	msg := &message.SendingMessage{Elements: elems}

	p.log.Debugln("Sending event", evt.ID, "to QQ")
	if info.SourceType == message.SourceGroup {
		ret := sender.Client.SendGroupMessage(target, msg)
		if ret == nil {
			p.log.Warnfln("Sending event", evt.ID, "to QQ failed")
		} else {
			msgKey := database.NewMessageKey(ret.Id, ret.InternalId)
			p.finishHandling(nil, msgKey, time.Unix(int64(ret.Time), 0), sender.UID, evt.ID, database.MsgNormal, database.MsgNoError, message.ToReadableString(elems))
		}
	} else {
		ret := sender.Client.SendPrivateMessage(target, msg)
		if ret == nil {
			p.log.Warnfln("Sending event", evt.ID, "to QQ failed")
		} else {
			msgKey := database.NewMessageKey(ret.Id, ret.InternalId)
			p.finishHandling(nil, msgKey, time.Unix(int64(ret.Time), 0), sender.UID, evt.ID, database.MsgNormal, database.MsgNoError, message.ToReadableString(elems))
		}
	}
}

func (p *Portal) HandleMatrixRedaction(sender *User, evt *event.Event) {
	msg := p.bridge.DB.Message.GetByMXID(evt.Redacts)
	if msg == nil || msg.IsFakeMsgID() {
		return
	}

	if p.IsPrivateChat() {
		if msg.Sender.Uin != sender.UID.Uin {
			return
		}

		if err := sender.Client.RecallPrivateMessage(
			p.Key.UID.IntUin(), msg.Timestamp.Unix(),
			int32(msg.Key.IntSeq()), int32(msg.Key.IntID()),
		); err != nil {
			p.log.Warnln("Failed to recall %s %s", evt.Redacts, msg.Key)
		}
	} else {
		if err := sender.Client.RecallGroupMessage(
			p.Key.UID.IntUin(), int32(msg.Key.IntSeq()), int32(msg.Key.IntID()),
		); err != nil {
			p.log.Warnln("Failed to recall %s %s", evt.Redacts, msg.Key)
		}
	}
}

func (p *Portal) HandleMatrixReaction(sender *User, evt *event.Event) {
	// TODO:
}

func (p *Portal) canBridgeFrom(sender *User) error {
	if !sender.IsLoggedIn() {
		if sender.Token != nil {
			return errUserNotConnected
		} else {
			return errUserNotLoggedIn
		}
	} else if p.IsPrivateChat() && sender.UID.Uin != p.Key.Receiver.Uin {
		return errDifferentUser
	}

	return nil
}

func (p *Portal) Delete() {
	p.Portal.Delete()
	p.bridge.portalsLock.Lock()
	delete(p.bridge.portalsByUID, p.Key)
	if len(p.MXID) > 0 {
		delete(p.bridge.portalsByMXID, p.MXID)
	}
	p.bridge.portalsLock.Unlock()
}

func (p *Portal) GetMatrixUsers() ([]id.UserID, error) {
	members, err := p.MainIntent().JoinedMembers(p.MXID)
	if err != nil {
		return nil, fmt.Errorf("failed to get member list: %w", err)
	}
	var users []id.UserID
	for userID := range members.Joined {
		_, isPuppet := p.bridge.ParsePuppetMXID(userID)
		if !isPuppet && userID != p.bridge.Bot.UserID {
			users = append(users, userID)
		}
	}

	return users, nil
}

func (p *Portal) CleanupIfEmpty() {
	users, err := p.GetMatrixUsers()
	if err != nil {
		p.log.Errorfln("Failed to get Matrix user list to determine if portal needs to be cleaned up: %v", err)
		return
	}

	if len(users) == 0 {
		p.log.Infoln("Room seems to be empty, cleaning up...")
		p.Delete()
		p.Cleanup(false)
	}
}

func (p *Portal) Cleanup(puppetsOnly bool) {
	if len(p.MXID) == 0 {
		return
	}
	intent := p.MainIntent()
	members, err := intent.JoinedMembers(p.MXID)
	if err != nil {
		p.log.Errorln("Failed to get portal members for cleanup:", err)
		return
	}
	for member := range members.Joined {
		if member == intent.UserID {
			continue
		}
		puppet := p.bridge.GetPuppetByMXID(member)
		if puppet != nil {
			_, err = puppet.DefaultIntent().LeaveRoom(p.MXID)
			if err != nil {
				p.log.Errorln("Error leaving as puppet while cleaning up portal:", err)
			}
		} else if !puppetsOnly {
			_, err = intent.KickUser(p.MXID, &mautrix.ReqKickUser{UserID: member, Reason: "Deleting portal"})
			if err != nil {
				p.log.Errorln("Error kicking user while cleaning up portal:", err)
			}
		}
	}
	_, err = intent.LeaveRoom(p.MXID)
	if err != nil {
		p.log.Errorln("Error leaving with main intent while cleaning up portal:", err)
	}
}

func (p *Portal) HandleMatrixLeave(brSender bridge.User) {
	// TODO:
}

func (p *Portal) HandleMatrixKick(brSender bridge.User, brTarget bridge.Ghost) {
	// TODO:
}

func (p *Portal) HandleMatrixInvite(brSender bridge.User, brTarget bridge.Ghost) {
	// TODO:
}

func (p *Portal) HandleMatrixMeta(brSender bridge.User, evt *event.Event) {
	// TODO:
}

func (br *QQBridge) GetPortalByMXID(mxid id.RoomID) *Portal {
	br.portalsLock.Lock()
	defer br.portalsLock.Unlock()

	portal, ok := br.portalsByMXID[mxid]
	if !ok {
		return br.loadDBPortal(br.DB.Portal.GetByMXID(mxid), nil)
	}

	return portal
}

func (br *QQBridge) GetIPortal(mxid id.RoomID) bridge.Portal {
	p := br.GetPortalByMXID(mxid)
	if p == nil {
		return nil
	}

	return p
}

func (br *QQBridge) GetPortalByUID(key database.PortalKey) *Portal {
	br.portalsLock.Lock()
	defer br.portalsLock.Unlock()

	portal, ok := br.portalsByUID[key]
	if !ok {
		return br.loadDBPortal(br.DB.Portal.GetByUID(key), &key)
	}

	return portal
}

func (br *QQBridge) GetAllPortals() []*Portal {
	return br.dbPortalsToPortals(br.DB.Portal.GetAll())
}

func (br *QQBridge) GetAllIPortals() (iportals []bridge.Portal) {
	portals := br.GetAllPortals()
	iportals = make([]bridge.Portal, len(portals))
	for i, portal := range portals {
		iportals[i] = portal
	}

	return iportals
}

func (br *QQBridge) GetAllPortalsByUID(uid types.UID) []*Portal {
	return br.dbPortalsToPortals(br.DB.Portal.GetAllByUID(uid))
}

func (br *QQBridge) dbPortalsToPortals(dbPortals []*database.Portal) []*Portal {
	br.portalsLock.Lock()
	defer br.portalsLock.Unlock()

	output := make([]*Portal, len(dbPortals))
	for index, dbPortal := range dbPortals {
		if dbPortal == nil {
			continue
		}
		portal, ok := br.portalsByUID[dbPortal.Key]
		if !ok {
			portal = br.loadDBPortal(dbPortal, nil)
		}
		output[index] = portal
	}

	return output
}

func (br *QQBridge) loadDBPortal(dbPortal *database.Portal, key *database.PortalKey) *Portal {
	if dbPortal == nil {
		if key == nil {
			return nil
		}
		dbPortal = br.DB.Portal.New()
		dbPortal.Key = *key
		dbPortal.Insert()
	}
	portal := br.NewPortal(dbPortal)
	br.portalsByUID[portal.Key] = portal
	if len(portal.MXID) > 0 {
		br.portalsByMXID[portal.MXID] = portal
	}

	return portal
}

func (br *QQBridge) newBlankPortal(key database.PortalKey) *Portal {
	portal := &Portal{
		bridge: br,
		log:    br.Log.Sub(fmt.Sprintf("Portal/%s", key)),

		messages:       make(chan PortalMessage, br.Config.Bridge.PortalMessageBuffer),
		matrixMessages: make(chan PortalMatrixMessage, br.Config.Bridge.PortalMessageBuffer),
	}

	go portal.handleMessageLoop()

	return portal
}

func (br *QQBridge) NewManualPortal(key database.PortalKey) *Portal {
	portal := br.newBlankPortal(key)
	portal.Portal = br.DB.Portal.New()
	portal.Key = key

	return portal
}

func (br *QQBridge) NewPortal(dbPortal *database.Portal) *Portal {
	portal := br.newBlankPortal(dbPortal.Key)
	portal.Portal = dbPortal

	return portal
}

func getAppView(elem *message.LightAppElement) string {
	return gjson.Get(elem.Content, "view").String()
}

func parseGeoURI(uri string) (lat, long float64, err error) {
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
	} else if long, err = strconv.ParseFloat(splitCoordinates[1], 64); err != nil {
		err = fmt.Errorf("longitude is not a number: %w", err)
	}
	return
}

func download(url string) ([]byte, *mimetype.MIME, error) {
	data, err := GetBytes(url)
	if err != nil {
		return nil, nil, err
	}

	return data, mimetype.Detect(data), nil
}
