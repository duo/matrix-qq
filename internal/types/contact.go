package types

import "strconv"

type ContactInfo struct {
	Uin    string
	Name   string
	Remark string
}

func NewContact(uin int64, name, remark string) *ContactInfo {
	return &ContactInfo{
		Uin:    strconv.FormatInt(uin, 10),
		Name:   name,
		Remark: remark,
	}
}
