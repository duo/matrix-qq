package database

import (
	"fmt"
	"math/rand"
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
		Seq: strconv.FormatInt(int64(rand.Intn(9999999999)), 10),
		ID:  id,
	}
}

func (mk MessageKey) IntSeq() int64 {
	number, _ := strconv.ParseInt(mk.Seq, 10, 64)
	return number
}

func (mk MessageKey) IntID() int64 {
	number, _ := strconv.ParseInt(mk.ID, 10, 64)
	return number
}

func (mk MessageKey) String() string {
	return fmt.Sprintf("%s%s%s", mk.Seq, SEP_PORTAL, mk.ID)
}
