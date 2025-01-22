package connector

import (
	"github.com/duo/matrix-qq/pkg/qqid"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func (qc *QQClient) selfEventSender() bridgev2.EventSender {
	return bridgev2.EventSender{
		IsFromMe:    true,
		Sender:      networkid.UserID(qc.UserLogin.ID),
		SenderLogin: qc.UserLogin.ID,
	}
}

func (qc *QQClient) makeEventSender(id string) bridgev2.EventSender {
	return bridgev2.EventSender{
		IsFromMe:    qqid.MakeUserLoginID(id) == qc.UserLogin.ID,
		Sender:      qqid.MakeUserID(id),
		SenderLogin: qqid.MakeUserLoginID(id),
	}
}

func (qc *QQClient) makePortalKey(chatType qqid.ChatType, chatID string) networkid.PortalKey {
	key := networkid.PortalKey{ID: networkid.PortalID(chatID)}
	// For non-group chats, add receiver
	if chatType != qqid.ChatGroup {
		key.Receiver = qc.UserLogin.ID
	}
	return key
}

func (qc *QQClient) makeDMPortalKey(identifier string) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(identifier),
		Receiver: qc.UserLogin.ID,
	}
}
