package connector

import (
	"fmt"

	"github.com/duo/matrix-qq/pkg/qqid"

	"github.com/LagrangeDev/LagrangeGo/client"
	"github.com/LagrangeDev/LagrangeGo/client/event"
	"github.com/LagrangeDev/LagrangeGo/message"
)

func (qc *QQClient) handlePrivateMessage(_ *client.QQClient, msg *message.PrivateMessage) {
	qc.UserLogin.Log.Trace().
		Any("message", msg).
		Msg("Receive QQ private message")

	qc.Main.Bridge.QueueRemoteEvent(qc.UserLogin, &QQMessageEvent{
		Message: &qqid.Message{
			ID:        fmt.Sprint(msg.ID),
			Timestamp: int64(msg.Time) * 1000,
			Type:      qqid.ParseMessageType(msg.Elements),
			ChatID:    fmt.Sprint(msg.Sender.Uin),
			ChatType:  qqid.ChatPrivate,
			SenderID:  fmt.Sprint(msg.Sender.Uin),
			Elements:  msg.Elements,
		},
		qc: qc,
	})
}

func (qc *QQClient) handleGroupMessage(_ *client.QQClient, msg *message.GroupMessage) {
	qc.UserLogin.Log.Trace().
		Any("message", msg).
		Msg("Receive QQ group message")

	qc.Main.Bridge.QueueRemoteEvent(qc.UserLogin, &QQMessageEvent{
		Message: &qqid.Message{
			ID:        fmt.Sprint(msg.ID),
			Timestamp: int64(msg.Time) * 1000,
			Type:      qqid.ParseMessageType(msg.Elements),
			ChatID:    fmt.Sprint(msg.GroupUin),
			ChatType:  qqid.ChatGroup,
			SenderID:  fmt.Sprint(msg.Sender.Uin),
			Elements:  msg.Elements,
		},
		qc: qc,
	})
}

func (qc *QQClient) handleFriendRecall(_ *client.QQClient, evt *event.FriendRecall) {
	qc.UserLogin.Log.Trace().
		Any("event", evt).
		Msg("Receive QQ friend recall event")

	qc.Main.Bridge.QueueRemoteEvent(qc.UserLogin, &QQMessageEvent{
		Message: &qqid.Message{
			ID:        fmt.Sprint(evt.Sequence),
			Timestamp: int64(evt.Time) * 1000,
			Type:      qqid.MsgRevoke,
			ChatID:    fmt.Sprint(evt.FromUin),
			ChatType:  qqid.ChatPrivate,
			SenderID:  fmt.Sprint(evt.FromUin),
			Elements:  make([]message.IMessageElement, 0),
		},
		qc: qc,
	})
}

func (qc *QQClient) handleGroupRecall(_ *client.QQClient, evt *event.GroupRecall) {
	qc.UserLogin.Log.Trace().
		Any("event", evt).
		Msg("Receive QQ group recall event")

	qc.Main.Bridge.QueueRemoteEvent(qc.UserLogin, &QQMessageEvent{
		Message: &qqid.Message{
			ID:        fmt.Sprint(evt.Sequence),
			Timestamp: int64(evt.Time) * 1000,
			Type:      qqid.MsgRevoke,
			ChatID:    fmt.Sprint(evt.GroupUin),
			ChatType:  qqid.ChatGroup,
			SenderID:  fmt.Sprint(evt.OperatorUin),
			Elements:  make([]message.IMessageElement, 0),
		},
		qc: qc,
	})
}
