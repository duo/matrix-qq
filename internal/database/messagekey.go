package database

import (
	"fmt"
	"strconv"
)

const SEP_MESSAGE = "\u0002"

type MessageKey struct {
	Seq string
	ID  string
}

func NewMessageKey(seq, id int32) MessageKey {
	return MessageKey{
		Seq: strconv.FormatInt(int64(seq), 10),
		ID:  strconv.FormatInt(int64(id), 10),
	}
}

func NewPartialKey(seq int64) MessageKey {
	return MessageKey{
		Seq: strconv.FormatInt(seq, 10),
	}
}

func NewFakeKey(id string) MessageKey {
	return MessageKey{
		ID: id,
	}
}

func (mk MessageKey) String() string {
	return fmt.Sprintf("%s%s%s", mk.Seq, SEP_PORTAL, mk.ID)
}
