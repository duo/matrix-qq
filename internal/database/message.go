package database

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/duo/matrix-qq/internal/types"

	"maunium.net/go/mautrix/id"
	"maunium.net/go/mautrix/util/dbutil"

	log "maunium.net/go/maulogger/v2"
)

type MessageErrorType string

const (
	MsgNoError             MessageErrorType = ""
	MsgErrDecryptionFailed MessageErrorType = "decryption_failed"
	MsgErrMediaNotFound    MessageErrorType = "media_not_found"
)

type MessageType string

const (
	MsgUnknown MessageType = ""
	MsgFake    MessageType = "fake"
	MsgNormal  MessageType = "message"
)

type Message struct {
	db  *Database
	log log.Logger

	Chat      PortalKey
	Key       MessageKey
	MXID      id.EventID
	Sender    types.UID
	Timestamp time.Time
	Sent      bool
	Type      MessageType
	Error     MessageErrorType
	Content   string
}

func (m *Message) IsFakeMXID() bool {
	return strings.HasPrefix(m.MXID.String(), "me.lxduo.qq.fake::")
}

func (m *Message) IsFakeMsgID() bool {
	return strings.HasPrefix(m.Key.ID, "FAKE::") || m.Key.ID == string(m.MXID)
}

func (m *Message) Scan(row dbutil.Scannable) *Message {
	var ts int64
	var content sql.NullString
	err := row.Scan(
		&m.Chat.UID, &m.Chat.Receiver, &m.Key.Seq, &m.Key.ID,
		&m.MXID, &m.Sender, &ts, &m.Sent, &m.Type, &m.Error, &content,
	)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			m.log.Errorln("Database scan failed:", err)
		}

		return nil
	}
	if ts != 0 {
		m.Timestamp = time.Unix(ts, 0)
	}
	if len(content.String) > 0 {
		m.Content = content.String
	} else {
		m.Content = ""
	}

	return m
}

func (m *Message) Insert(txn dbutil.Transaction) {
	query := `
		INSERT INTO message
			(chat_uid, chat_receiver, msg_seq, msg_id, mxid, sender, timestamp, sent, type, error, content)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	args := []interface{}{
		m.Chat.UID, m.Chat.Receiver, m.Key.Seq, m.Key.ID, m.MXID,
		m.Sender, m.Timestamp.Unix(), m.Sent, m.Type, m.Error, m.Content,
	}

	var err error
	if txn != nil {
		_, err = txn.Exec(query, args...)
	} else {
		_, err = m.db.Exec(query, args...)
	}
	if err != nil {
		m.log.Warnfln("Failed to insert %s %s: %v", m.Chat, m.Key, err)
	}
}

func (m *Message) UpdateMXID(txn dbutil.Transaction, mxid id.EventID, newType MessageType, newError MessageErrorType) {
	m.MXID = mxid
	m.Type = newType
	m.Error = newError

	query := `
		UPDATE message
		SET mxid=$1, type=$2, error=$3
		WHERE chat_uid=$4 AND chat_receiver=$5 AND msg_seq=$6 AND msg_id=$7
	`
	args := []interface{}{
		mxid, newType, newError, m.Chat.UID, m.Chat.Receiver, m.Key.Seq, m.Key.ID,
	}

	var err error
	if txn != nil {
		_, err = txn.Exec(query, args...)
	} else {
		_, err = m.db.Exec(query, args...)
	}
	if err != nil {
		m.log.Warnfln("Failed to update %s %s: %v", m.Chat, m.Key, err)
	}
}

func (m *Message) Delete() {
	query := `
		DELETE FROM message
		WHERE chat_uid=$1 AND chat_receiver=$2 AND msg_seq=$3 AND msg_id=$4
	`
	args := []interface{}{
		m.Chat.UID, m.Chat.Receiver, m.Key.Seq, m.Key.ID,
	}
	_, err := m.db.Exec(query, args...)
	if err != nil {
		m.log.Warnfln("Failed to delete %s %s: %v", m.Chat, m.Key, err)
	}
}
