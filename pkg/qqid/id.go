package qqid

import (
	"fmt"
	"strings"

	"github.com/LagrangeDev/LagrangeGo/message"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type ChatType int

const (
	ChatUnknown ChatType = iota
	ChatPrivate
	ChatGroup
	ChatTemp
)

type MessageType string

const (
	MsgText     MessageType = "text"
	MsgImage    MessageType = "image"
	MsgSticker  MessageType = "sticker"
	MsgAudio    MessageType = "audio"
	MsgVideo    MessageType = "video"
	MsgFile     MessageType = "file"
	MsgLocation MessageType = "location"
	MsgApp      MessageType = "app"
	MsgRevoke   MessageType = "revoke"
)

type Message struct {
	ID        string
	Timestamp int64
	Type      MessageType
	ChatID    string
	ChatType  ChatType
	SenderID  string

	Elements []message.IMessageElement
}

func ParseMessageType(elems []message.IMessageElement) MessageType {
	for _, elem := range elems {
		switch elem.Type() {
		case message.Image:
			return MsgImage
		case message.Voice:
			return MsgAudio
		case message.Video:
			return MsgVideo
		case message.File:
			return MsgFile
		case message.Forward:
		case message.Service, message.LightApp:
			return MsgApp
		}
	}

	return MsgText
}

func MakeUserID(id string) networkid.UserID {
	return networkid.UserID(id)
}

func MakeUserLoginID(id string) networkid.UserLoginID {
	return networkid.UserLoginID(id)
}

func MakeMessageID(chat string, id string) networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("%s:%s", chat, id))
}

func MakeFakeMessageID(chat string, data string) networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("fake:%s:%s", chat, data))
}

type ParsedMessageID struct {
	Chat string
	ID   string
}

func ParseMessageID(messageID networkid.MessageID) (*ParsedMessageID, error) {
	parts := strings.SplitN(string(messageID), ":", 2)
	if len(parts) == 2 {
		if parts[0] == "fake" {
			return nil, fmt.Errorf("fake message ID")
		}
		return &ParsedMessageID{Chat: parts[0], ID: parts[1]}, nil
	} else {
		return nil, fmt.Errorf("invalid message ID")
	}
}
