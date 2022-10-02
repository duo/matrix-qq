package database

import (
	"maunium.net/go/mautrix/id"

	log "maunium.net/go/maulogger/v2"
)

type MessageQuery struct {
	db  *Database
	log log.Logger
}

func (mq *MessageQuery) New() *Message {
	return &Message{
		db:  mq.db,
		log: mq.log,
	}
}

const (
	getAllMessagesQuery = `
		SELECT chat_uid, chat_receiver, msg_seq, msg_id, mxid, sender, timestamp, sent, type, error, content
		FROM message
		WHERE chat_uid=$1 AND chat_receiver=$2
	`
	getMessageByMsgKeyQuery = `
		SELECT chat_uid, chat_receiver, msg_seq, msg_id, mxid, sender, timestamp, sent, type, error, content
		FROM message
		WHERE chat_uid=$1 AND chat_receiver=$2 AND msg_seq=$3 AND msg_id=$4
	`
	getMessageByReplyQuery = `
		SELECT chat_uid, chat_receiver, msg_seq, msg_id, mxid, sender, timestamp, sent, type, error, content
		FROM message
		WHERE chat_uid=$1 AND chat_receiver=$2 AND msg_seq=$3 AND timestamp=$4 LIMIT 1
	`
	getMessageByReplyBackQuery = `
		SELECT chat_uid, chat_receiver, msg_seq, msg_id, mxid, sender, timestamp, sent, type, error, content
		FROM message
		WHERE chat_uid=$1 AND chat_receiver=$2 AND msg_seq=$3 AND timestamp>$4 LIMIT 1
	`
	getMessageByMXIDQuery = `
		SELECT chat_uid, chat_receiver, msg_seq, msg_id, mxid, sender, timestamp, sent, type, error, content
		FROM message
		WHERE mxid=$1
	`
)

func (mq *MessageQuery) GetAll(chat PortalKey) []*Message {
	messages := []*Message{}

	rows, err := mq.db.Query(getAllMessagesQuery, chat.UID, chat.Receiver)
	if err != nil || rows == nil {
		return messages
	}
	for rows.Next() {
		messages = append(messages, mq.New().Scan(rows))
	}

	return messages
}

func (mq *MessageQuery) GetByMessageKey(chat PortalKey, key MessageKey) *Message {
	row := mq.db.QueryRow(getMessageByMsgKeyQuery, chat.UID, chat.Receiver, key.Seq, key.ID)
	if row == nil {
		return nil
	}

	return mq.New().Scan(row)
}

func (mq *MessageQuery) GetByReply(chat PortalKey, msgSeq string, ts int64) *Message {
	row := mq.db.QueryRow(getMessageByReplyQuery, chat.UID, chat.Receiver, msgSeq, ts)
	if row == nil {
		return nil
	}

	msg := mq.New().Scan(row)
	if msg == nil {
		// FIXME: how to map the correct one!
		row = mq.db.QueryRow(getMessageByReplyBackQuery, chat.UID, chat.Receiver, msgSeq, ts-2*60)
		if row == nil {
			return nil
		}
		msg = mq.New().Scan(row)
	}

	return msg
}

func (mq *MessageQuery) GetByMXID(mxid id.EventID) *Message {
	row := mq.db.QueryRow(getMessageByMXIDQuery, mxid)
	if row == nil {
		return nil
	}

	return mq.New().Scan(row)
}
