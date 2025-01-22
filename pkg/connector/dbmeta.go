package connector

import (
	"github.com/duo/matrix-qq/pkg/qqid"

	"maunium.net/go/mautrix/bridgev2/database"
)

func (qc *QQConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Portal: func() any {
			return &qqid.PortalMetadata{}
		},
		Ghost: func() any {
			return &qqid.GhostMetadata{}
		},
		Message:  nil,
		Reaction: nil,
		UserLogin: func() any {
			return &qqid.UserLoginMetadata{}
		},
	}
}
