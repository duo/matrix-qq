package connector

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LagrangeDev/LagrangeGo/client"
	"github.com/duo/matrix-qq/pkg/qqid"
	"maunium.net/go/mautrix/bridge/status"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type resyncQueueItem struct {
	portal *bridgev2.Portal
	ghost  *bridgev2.Ghost
}

type QQClient struct {
	Main      *QQConnector
	UserLogin *bridgev2.UserLogin
	Client    *client.QQClient

	stopLoops       atomic.Pointer[context.CancelFunc]
	resyncQueue     map[string]resyncQueueItem
	resyncQueueLock sync.Mutex
	nextResync      time.Time
}

var (
	_ bridgev2.NetworkAPI                    = (*QQClient)(nil)
	_ bridgev2.IdentifierResolvingNetworkAPI = (*QQClient)(nil)
)

func (qc *QQClient) Connect(ctx context.Context) {
	if qc.Client == nil {
		state := status.BridgeState{
			StateEvent: status.StateBadCredentials,
			Message:    "You're not logged into QQ",
		}
		qc.UserLogin.BridgeState.Send(state)
		return
	}

	qc.Client.PrivateMessageEvent.Subscribe(qc.handlePrivateMessage)
	qc.Client.GroupMessageEvent.Subscribe(qc.handleGroupMessage)
	qc.Client.FriendRecallEvent.Subscribe(qc.handleFriendRecall)
	qc.Client.GroupRecallEvent.Subscribe(qc.handleGroupRecall)

	qc.Client.RefreshFriendCache()
	qc.Client.RefreshAllGroupsInfo()
	qc.Client.RefreshAllGroupMembersCache()

	qc.startLoops()
}

func (qc *QQClient) Disconnect() {
	// Stop sync
	if stopSyncLoop := qc.stopLoops.Swap(nil); stopSyncLoop != nil {
		(*stopSyncLoop)()
	}

	if cli := qc.Client; cli != nil {
		cli.Release()
		qc.Client = nil
	}
}

func (qc *QQClient) LogoutRemote(ctx context.Context) {
	qc.Disconnect()

	qc.UserLogin.Metadata = &qqid.UserLoginMetadata{}
	qc.UserLogin.Save(ctx)
}

func (qc *QQClient) IsLoggedIn() bool {
	return qc.Client.Online.Load()
}

func (qc *QQClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	return networkid.UserLoginID(userID) == qc.UserLogin.ID
}

func (qc *QQClient) startLoops() {
	ctx, cancel := context.WithCancel(context.Background())
	oldStop := qc.stopLoops.Swap(&cancel)
	if oldStop != nil {
		(*oldStop)()
	}

	go qc.ghostResyncLoop(ctx)
}
