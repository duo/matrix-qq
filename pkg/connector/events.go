package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/duo/matrix-qq/pkg/qqid"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type QQMessageEvent struct {
	Message    *qqid.Message
	qc         *QQClient
	postHandle func()
}

var (
	_ bridgev2.RemoteEventThatMayCreatePortal = (*QQMessageEvent)(nil)
	_ bridgev2.RemoteChatResyncWithInfo       = (*QQMessageEvent)(nil)
	_ bridgev2.RemoteMessage                  = (*QQMessageEvent)(nil)
	_ bridgev2.RemoteEventWithTimestamp       = (*QQMessageEvent)(nil)
	_ bridgev2.RemoteMessageRemove            = (*QQMessageEvent)(nil)
	_ bridgev2.RemotePostHandler              = (*QQMessageEvent)(nil)
)

func (evt *QQMessageEvent) ShouldCreatePortal() bool {
	return true
}

func (evt *QQMessageEvent) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("message_id", evt.Message.ID).Str("sender_id", evt.Message.SenderID)
}

func (evt *QQMessageEvent) GetPortalKey() networkid.PortalKey {
	return evt.qc.makePortalKey(evt.Message.ChatType, evt.Message.ChatID)
}

func (evt *QQMessageEvent) GetSender() bridgev2.EventSender {
	return evt.qc.makeEventSender(evt.Message.SenderID)
}

func (evt *QQMessageEvent) GetID() networkid.MessageID {
	if evt.Message.Type == qqid.MsgRevoke {
		return qqid.MakeFakeMessageID(evt.Message.ChatID, "revoke-"+evt.Message.ID)
	}
	return qqid.MakeMessageID(evt.Message.ChatID, evt.Message.ID)
}

func (evt *QQMessageEvent) GetTimestamp() time.Time {
	return time.UnixMilli(evt.Message.Timestamp)
}

func (evt *QQMessageEvent) GetType() bridgev2.RemoteEventType {
	/*
		if evt.Message.Type == qqid.MsgRevoke {
			return bridgev2.RemoteEventMessageRemove
		}
	*/
	return bridgev2.RemoteEventMessage
}

func (evt *QQMessageEvent) GetTargetMessage() networkid.MessageID {
	return qqid.MakeMessageID(evt.Message.ChatID, evt.Message.ID)
}

func (evt *QQMessageEvent) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	switch evt.Message.ChatType {
	case qqid.ChatPrivate:
		return evt.qc.getDirectChatInfo(string(portal.ID))
	case qqid.ChatGroup:
		if portal.MXID == "" {
			evt.postHandle = func() {
				evt.qc.updateMemberDisplyname(ctx, portal)
			}
		}
		return evt.qc.getGroupChatInfo(ctx, portal)
	default:
		return nil, fmt.Errorf("chat type %v not supported", evt.Message.ChatType)
	}
}

func (evt *QQMessageEvent) PostHandle(ctx context.Context, portal *bridgev2.Portal) {
	if ph := evt.postHandle; ph != nil {
		evt.postHandle = nil
		ph()
	}
}

func (evt *QQMessageEvent) ConvertMessage(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) (*bridgev2.ConvertedMessage, error) {
	evt.qc.EnqueuePortalResync(portal)

	return evt.qc.Main.MsgConv.ToMatrix(ctx, evt.qc.Client, portal, intent, evt.Message), nil
}
