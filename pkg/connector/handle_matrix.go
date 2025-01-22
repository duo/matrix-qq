package connector

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/duo/matrix-qq/pkg/qqid"

	"github.com/LagrangeDev/LagrangeGo/message"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

func (qc *QQClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if !qc.IsLoggedIn() {
		return nil, bridgev2.ErrNotLoggedIn
	}

	elements, err := qc.Main.MsgConv.ToQQ(ctx, qc.Client, msg.Event, msg.Content, msg.Portal)
	if err != nil {
		return nil, fmt.Errorf("failed to convert message: %w", err)
	}

	if msg.ReplyTo != nil {
		if msgID, err := qqid.ParseMessageID(msg.ReplyTo.ID); err != nil {
			return nil, err
		} else {
			id, _ := strconv.ParseUint(msgID.ID, 10, 32)
			sender, _ := strconv.ParseUint(string(msg.ReplyTo.SenderID), 10, 32)
			elements = append(
				[]message.IMessageElement{
					&message.ReplyElement{ReplySeq: uint32(id)},
					message.NewAt(uint32(sender)),
				},
				elements...,
			)
		}
	}

	target, _ := strconv.ParseUint(string(msg.Portal.ID), 10, 32)

	meta := msg.Portal.Metadata.(*qqid.PortalMetadata)
	switch meta.ChatType {
	case qqid.ChatPrivate:
		if resp, err := qc.Client.SendPrivateMessage(uint32(target), elements); err != nil {
			return nil, bridgev2.WrapErrorInStatus(err).WithSendNotice(true)
		} else if resp == nil {
			return nil, bridgev2.WrapErrorInStatus(fmt.Errorf("sent message return empty respose")).WithSendNotice(true)
		} else {
			return &bridgev2.MatrixMessageResponse{
				DB: &database.Message{
					ID:        qqid.MakeMessageID(string(msg.Portal.ID), fmt.Sprint(resp.ID)),
					SenderID:  qqid.MakeUserID(fmt.Sprint(resp.Sender.Uin)),
					Timestamp: time.UnixMilli(int64(resp.Time) * 1000),
				},
				StreamOrder: time.UnixMilli(int64(resp.Time) * 1000).Unix(),
			}, nil
		}
	case qqid.ChatGroup:
		if resp, err := qc.Client.SendGroupMessage(uint32(target), elements); err != nil {
			return nil, bridgev2.WrapErrorInStatus(err).WithSendNotice(true)
		} else {
			return &bridgev2.MatrixMessageResponse{
				DB: &database.Message{
					ID:        qqid.MakeMessageID(string(msg.Portal.ID), fmt.Sprint(resp.ID)),
					SenderID:  qqid.MakeUserID(fmt.Sprint(resp.Sender.Uin)),
					Timestamp: time.UnixMilli(int64(resp.Time) * 1000),
				},
				StreamOrder: time.UnixMilli(int64(resp.Time) * 1000).Unix(),
			}, nil
		}
	default:
		return nil, fmt.Errorf("unknown chat type")
	}
}
