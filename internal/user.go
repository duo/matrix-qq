package internal

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/duo/matrix-qq/internal/database"
	"github.com/duo/matrix-qq/internal/types"
	"github.com/tidwall/gjson"

	"github.com/Mrs4s/MiraiGo/client"
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/Mrs4s/MiraiGo/wrapper"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/bridge"
	"maunium.net/go/mautrix/bridge/bridgeconfig"
	"maunium.net/go/mautrix/bridge/commands"
	"maunium.net/go/mautrix/bridge/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	log "maunium.net/go/maulogger/v2"
)

const (
	resyncMinInterval  = 7 * 24 * time.Hour
	resyncLoopInterval = 4 * time.Hour
	emptyAvatar        = "acef72340ac0e914090bd35799f5594e"
)

var (
	ErrAlreadyLoggedIn = errors.New("already logged in")

	deviceLock sync.Mutex
)

type resyncQueueItem struct {
	portal *Portal
	puppet *Puppet
}

type User struct {
	*database.User

	Client      *client.QQClient
	reLoginLock sync.Mutex

	bridge *QQBridge
	log    log.Logger

	Admin           bool
	Whitelisted     bool
	PermissionLevel bridgeconfig.PermissionLevel

	mgmtCreateLock  sync.Mutex
	spaceCreateLock sync.Mutex
	connLock        sync.Mutex

	spaceMembershipChecked bool

	BridgeState *bridge.BridgeStateQueue

	resyncQueue     map[types.UID]resyncQueueItem
	resyncQueueLock sync.Mutex
	nextResync      time.Time

	commadnState *commands.CommandState
}

func (u *User) GetPermissionLevel() bridgeconfig.PermissionLevel {
	return u.PermissionLevel
}

func (u *User) GetManagementRoomID() id.RoomID {
	return u.ManagementRoom
}

func (u *User) GetMXID() id.UserID {
	return u.MXID
}

func (u *User) SetCommandState(s *commands.CommandState) {
	u.commadnState = s
}

func (u *User) GetCommandState() *commands.CommandState {
	return u.commadnState
}

func (u *User) addToUIDMap() {
	u.bridge.usersLock.Lock()
	u.bridge.usersByUsername[u.UID.Uin] = u
	u.bridge.usersLock.Unlock()
}

func (u *User) removeFromUIDMap(state status.BridgeState) {
	u.bridge.usersLock.Lock()
	uidUser, ok := u.bridge.usersByUsername[u.UID.Uin]
	if ok && u == uidUser {
		delete(u.bridge.usersByUsername, u.UID.Uin)
	}
	u.bridge.usersLock.Unlock()
	u.BridgeState.Send(state)
}
func (u *User) puppetResyncLoop() {
	u.nextResync = time.Now().Add(resyncLoopInterval).Add(-time.Duration(rand.Intn(3600)) * time.Second)
	for {
		time.Sleep(time.Until(u.nextResync))
		u.nextResync = time.Now().Add(resyncLoopInterval)
		u.doPuppetResync()
	}
}

func (u *User) EnqueuePuppetResync(puppet *Puppet) {
	if puppet.LastSync.Add(resyncMinInterval).After(time.Now()) {
		return
	}
	u.resyncQueueLock.Lock()
	if _, exists := u.resyncQueue[puppet.UID]; !exists {
		u.resyncQueue[puppet.UID] = resyncQueueItem{puppet: puppet}
		u.log.Debugfln("Enqueued resync for %s (next sync in %s)", puppet.UID, time.Until(u.nextResync))
	}
	u.resyncQueueLock.Unlock()
}

func (u *User) EnqueuePortalResync(portal *Portal) {
	if !portal.IsGroupChat() || portal.LastSync.Add(resyncMinInterval).After(time.Now()) {
		return
	}
	u.resyncQueueLock.Lock()
	if _, exists := u.resyncQueue[portal.Key.UID]; !exists {
		u.resyncQueue[portal.Key.UID] = resyncQueueItem{portal: portal}
		u.log.Debugfln("Enqueued resync for %s (next sync in %s)", portal.Key.UID, time.Until(u.nextResync))
	}
	u.resyncQueueLock.Unlock()
}

func (u *User) doPuppetResync() {
	if !u.IsLoggedIn() {
		return
	}
	u.resyncQueueLock.Lock()
	if len(u.resyncQueue) == 0 {
		u.resyncQueueLock.Unlock()
		return
	}
	queue := u.resyncQueue
	u.resyncQueue = make(map[types.UID]resyncQueueItem)
	u.resyncQueueLock.Unlock()
	var puppets []*Puppet
	var portals []*Portal
	for uid, item := range queue {
		var lastSync time.Time
		if item.puppet != nil {
			lastSync = item.puppet.LastSync
		} else if item.portal != nil {
			lastSync = item.portal.LastSync
		}
		if lastSync.Add(resyncMinInterval).After(time.Now()) {
			u.log.Debugfln("Not resyncing %s, last sync was %s ago", uid, time.Until(lastSync))
			continue
		}
		if item.puppet != nil {
			puppets = append(puppets, item.puppet)
		} else if item.portal != nil {
			portals = append(portals, item.portal)
		}
	}
	for _, portal := range portals {
		groupInfo := u.Client.FindGroup(portal.Key.Receiver.IntUin())
		if groupInfo != nil {
			m, err := u.Client.GetGroupMembers(groupInfo)
			if err != nil {
				u.log.Warnfln("Failed to get group members for %s to do background sync", portal.Key.UID)
			} else {
				groupInfo.Members = m
				u.log.Debugfln("Doing background sync for %s", portal.Key.UID)
				portal.UpdateMatrixRoom(u, groupInfo, false)
			}
		} else {
			u.log.Warnfln("Failed to get group info for %s to do background sync", portal.Key.UID)
		}
	}
	for _, puppet := range puppets {
		u.log.Debugfln("Doing background sync for user: %v", puppet.UID)
		friend := u.Client.FindFriend(puppet.UID.IntUin())
		if friend != nil {
			puppet.Sync(u, types.NewContact(friend.Uin, friend.Nickname, ""), true, true)
		} else {
			summary, err := u.Client.GetSummaryInfo(puppet.UID.IntUin())
			if err != nil {
				u.log.Warnfln("Failed to get contact info for %s in background sync: %v", puppet.UID, err)
			} else {
				puppet.Sync(u, types.NewContact(summary.Uin, summary.Nickname, ""), true, true)
			}
		}
	}
}

func (u *User) ensureInvited(intent *appservice.IntentAPI, roomID id.RoomID, isDirect bool) (ok bool) {
	extraContent := make(map[string]interface{})
	if isDirect {
		extraContent["is_direct"] = true
	}
	customPuppet := u.bridge.GetPuppetByCustomMXID(u.MXID)
	if customPuppet != nil && customPuppet.CustomIntent() != nil {
		extraContent["me.lxduo.qq.will_auto_accept"] = true
	}
	_, err := intent.InviteUser(roomID, &mautrix.ReqInviteUser{UserID: u.MXID}, extraContent)
	var httpErr mautrix.HTTPError
	if err != nil && errors.As(err, &httpErr) && httpErr.RespError != nil && strings.Contains(httpErr.RespError.Err, "is already in the room") {
		u.bridge.StateStore.SetMembership(roomID, u.MXID, event.MembershipJoin)
		ok = true
		return
	} else if err != nil {
		u.log.Warnfln("Failed to invite user to %s: %v", roomID, err)
	} else {
		ok = true
	}

	if customPuppet != nil && customPuppet.CustomIntent() != nil {
		err = customPuppet.CustomIntent().EnsureJoined(roomID, appservice.EnsureJoinedParams{IgnoreCache: true})
		if err != nil {
			u.log.Warnfln("Failed to auto-join %s: %v", roomID, err)
			ok = false
		} else {
			ok = true
		}
	}
	return
}

func (u *User) GetSpaceRoom() id.RoomID {
	if !u.bridge.Config.Bridge.PersonalFilteringSpaces {
		return ""
	}

	if len(u.SpaceRoom) == 0 {
		u.spaceCreateLock.Lock()
		defer u.spaceCreateLock.Unlock()
		if len(u.SpaceRoom) > 0 {
			return u.SpaceRoom
		}

		resp, err := u.bridge.Bot.CreateRoom(&mautrix.ReqCreateRoom{
			Visibility: "private",
			Name:       "QQ",
			Topic:      "Your QQ bridged chats",
			InitialState: []*event.Event{{
				Type: event.StateRoomAvatar,
				Content: event.Content{
					Parsed: &event.RoomAvatarEventContent{
						URL: u.bridge.Config.AppService.Bot.ParsedAvatar,
					},
				},
			}},
			CreationContent: map[string]interface{}{
				"type": event.RoomTypeSpace,
			},
			PowerLevelOverride: &event.PowerLevelsEventContent{
				Users: map[id.UserID]int{
					u.bridge.Bot.UserID: 9001,
					u.MXID:              50,
				},
			},
		})

		if err != nil {
			u.log.Errorln("Failed to auto-create space room:", err)
		} else {
			u.SpaceRoom = resp.RoomID
			u.Update()
			u.ensureInvited(u.bridge.Bot, u.SpaceRoom, false)
		}
	} else if !u.spaceMembershipChecked && !u.bridge.StateStore.IsInRoom(u.SpaceRoom, u.MXID) {
		u.ensureInvited(u.bridge.Bot, u.SpaceRoom, false)
	}
	u.spaceMembershipChecked = true

	return u.SpaceRoom
}

func (u *User) GetManagementRoom() id.RoomID {
	if len(u.ManagementRoom) == 0 {
		u.mgmtCreateLock.Lock()
		defer u.mgmtCreateLock.Unlock()

		if len(u.ManagementRoom) > 0 {
			return u.ManagementRoom
		}
		creationContent := make(map[string]interface{})
		if !u.bridge.Config.Bridge.FederateRooms {
			creationContent["m.federate"] = false
		}
		resp, err := u.bridge.Bot.CreateRoom(&mautrix.ReqCreateRoom{
			Topic:           "QQ bridge notices",
			IsDirect:        true,
			CreationContent: creationContent,
		})
		if err != nil {
			u.log.Errorln("Failed to auto-create management room:", err)
		} else {
			u.SetManagementRoom(resp.RoomID)
		}
	}

	return u.ManagementRoom
}

func (u *User) SetManagementRoom(roomID id.RoomID) {
	existingUser, ok := u.bridge.managementRooms[roomID]
	if ok {
		existingUser.ManagementRoom = ""
		existingUser.Update()
	}

	u.ManagementRoom = roomID
	u.bridge.managementRooms[u.ManagementRoom] = u
	u.Update()
}

func (u *User) failedConnect(err error) {
	u.log.Warnln("Error connecting to QQ:", err)
	u.Token = nil
	u.Update()
	u.Client.Disconnect()
	u.Client.Release()
	u.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateUnknownError,
		Error:      QQConnectionFailed,
		Info: map[string]interface{}{
			"go_error": err.Error(),
		},
	})
	u.BridgeState.Send(status.BridgeState{StateEvent: status.StateUnknownError, Error: QQConnectionFailed})
}

func (u *User) createClient() {
	deviceLock.Lock()
	defer deviceLock.Unlock()

	device := &client.DeviceInfo{}
	if len(u.Device) == 0 {
		device = client.GenRandomDevice()
	} else {
		if err := device.ReadJson([]byte(u.Device)); err != nil {
			u.log.Warnfln("failed to load device information: %v", err)
			device = client.GenRandomDevice()
			u.Token = nil
		}
	}
	setClientProtocol(device, u.bridge.Config.QQ.Protocol)
	u.Device = string(device.ToJson())

	if u.bridge.Config.QQ.SignServer != "" {
		wrapper.DandelionEnergy = func(uin uint64, id, appVersion string, salt []byte) ([]byte, error) {
			return energy(u.bridge.Config.QQ.SignServer, uin, id, appVersion, salt)
		}
		wrapper.FekitGetSign = func(seq uint64, uin, cmd, qua string, buff []byte) ([]byte, []byte, []byte, error) {
			return sign(u.bridge.Config.QQ.SignServer, seq, uin, cmd, qua, buff)
		}
	}

	u.Client = client.NewClientEmpty()
	u.Client.UseDevice(device)
	u.Client.PrivateMessageEvent.Subscribe(u.handlePrivateMessage)
	u.Client.GroupMessageEvent.Subscribe(u.handleGroupMessage)
	u.Client.SelfPrivateMessageEvent.Subscribe(u.handlePrivateMessage)
	u.Client.SelfGroupMessageEvent.Subscribe(u.handleGroupMessage)
	u.Client.TempMessageEvent.Subscribe(u.handleTempMessage)
	u.Client.OfflineFileEvent.Subscribe(u.handleOfflineFileEvent)
	u.Client.GroupInvitedEvent.Subscribe(u.handleGroupJoin)
	u.Client.GroupLeaveEvent.Subscribe(u.handleGroupLeave)
	u.Client.GroupMemberJoinEvent.Subscribe(u.handleGroupMemberJoin)
	u.Client.GroupMemberLeaveEvent.Subscribe(u.handleGroupMemberLeave)
	u.Client.GroupMuteEvent.Subscribe(u.handleGroupMute)
	u.Client.GroupMessageRecalledEvent.Subscribe(u.handleGroupRecalled)
	u.Client.FriendMessageRecalledEvent.Subscribe(u.handleFriendRecalled)
	u.Client.MemberCardUpdatedEvent.Subscribe(u.handleMemberCardUpdated)

	u.Client.DisconnectedEvent.Subscribe(func(q *client.QQClient, e *client.ClientDisconnectedEvent) {
		u.reLoginLock.Lock()
		defer u.reLoginLock.Unlock()

		u.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnecting, Error: QQConnecting})
		if u.Client.Online.Load() {
			return
		}

		u.log.Warnfln("QQ %s is offline: %v", u.UID, e.Message)
		for {
			time.Sleep(time.Second * time.Duration(5))

			if u.Client.Online.Load() {
				u.log.Infofln("QQ %s re-login complete.", u.UID)
				break
			}

			err := u.Client.TokenLogin(u.Token)
			if err == nil {
				u.Token = u.Client.GenToken()
				u.Update()
				return
			} else {
				u.log.Warnfln("QQ %s re-login failed: %v", u.UID, err)
				u.BridgeState.Send(status.BridgeState{StateEvent: status.StateUnknownError, Error: QQConnectionFailed})
			}
		}
	})
}

func (u *User) LoginPassword(uin int64, password string) (*client.LoginResponse, error) {
	u.connLock.Lock()
	defer u.connLock.Unlock()

	if u.IsLoggedIn() {
		return nil, ErrAlreadyLoggedIn
	} else if u.Client != nil {
		u.unlockedDeleteConnection()
	}

	u.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnecting, Error: QQConnecting})
	u.createClient()

	u.Client.Uin = uin
	u.Client.PasswordMd5 = md5.Sum([]byte(password))

	ret, err := u.Client.Login()
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (u *User) LoginToken(encodedDevice, encodedToken string) error {
	u.connLock.Lock()
	defer u.connLock.Unlock()

	if u.IsLoggedIn() {
		return ErrAlreadyLoggedIn
	} else if u.Client != nil {
		u.unlockedDeleteConnection()
	}

	deviceBytes, err := base64.StdEncoding.DecodeString(encodedDevice)
	if err != nil {
		return err
	}
	tokenBytes, err := base64.StdEncoding.DecodeString(encodedToken)
	if err != nil {
		return err
	}

	u.Device = string(deviceBytes)
	u.Token = tokenBytes

	u.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnecting, Error: QQConnecting})
	u.createClient()

	if err := u.Client.TokenLogin(u.Token); err != nil {
		u.failedConnect(err)
		return err
	}

	u.MarkLogin()

	return nil
}

func (u *User) LoginQR() (<-chan *client.QRCodeLoginResponse, error) {
	u.connLock.Lock()
	defer u.connLock.Unlock()

	if u.IsLoggedIn() {
		return nil, ErrAlreadyLoggedIn
	} else if u.Client != nil {
		u.unlockedDeleteConnection()
	}

	u.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnecting, Error: QQConnecting})
	u.createClient()

	qrChan := make(chan *client.QRCodeLoginResponse, 256)

	ret, err := u.Client.FetchQRCodeCustomSize(1, 2, 1)
	if err != nil {
		u.log.Warnfln("Fetch QQ QR Code failed: %v", err)
		return nil, err
	}

	go func() {
		defer func() {
			if err := recover(); err != nil {
				u.log.Errorfln("Login qq panic: %v", err)
			}
		}()

		qrChan <- ret

		for {
			time.Sleep(time.Second)

			s, _ := u.Client.QueryQRCodeStatus(ret.Sig)
			if s == nil {
				continue
			}
			qrChan <- s

			if s.State == client.QRCodeConfirmed {
				r, err := u.Client.QRCodeLogin(s.LoginInfo)
				if err != nil || !r.Success {
					u.log.Warnfln("Failed to qr login: %v", err)
					u.BridgeState.Send(status.BridgeState{StateEvent: status.StateUnknownError, Error: QQConnectionFailed})

					qrChan <- &client.QRCodeLoginResponse{State: 0}
					close(qrChan)
				} else {
					u.MarkLogin()
					close(qrChan)
				}
				return
			}
		}
	}()

	return qrChan, nil
}

func (u *User) MarkLogin() {
	u.UID = types.NewIntUserUID(u.Client.Uin)
	u.Token = u.Client.GenToken()
	u.addToUIDMap()
	u.Update()

	go u.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
	go u.tryAutomaticDoublePuppeting()
}

func (u *User) Connect() bool {
	u.connLock.Lock()
	defer u.connLock.Unlock()

	if u.Client != nil {
		return u.Client.Online.Load()
	} else if u.Token == nil {
		return false
	}

	u.log.Debugln("Connecting to QQ %s", u.UID)
	u.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnecting, Error: QQConnecting})
	u.createClient()

	if err := u.Client.TokenLogin(u.Token); err != nil {
		u.failedConnect(err)
		return false
	}

	u.Token = u.Client.GenToken()
	u.Update()
	u.Client.AllowSlider = true
	if err := u.Client.ReloadFriendList(); err != nil {
		u.failedConnect(err)
		return false
	}
	if err := u.Client.ReloadGroupList(); err != nil {
		u.failedConnect(err)
		return false
	}

	go u.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
	go u.tryAutomaticDoublePuppeting()

	u.log.Debugln("Login to QQ %s", u.UID)

	return true
}

func (u *User) unlockedDeleteConnection() {
	if u.Client == nil {
		return
	}
	u.Client.Disconnect()
	u.Client.Release()
	u.Client = nil
}

func (u *User) DeleteConnection() {
	u.connLock.Lock()
	defer u.connLock.Unlock()
	u.unlockedDeleteConnection()
}

func (u *User) DeleteSession() {
	u.Device = ""
	u.Token = nil
	if !u.UID.IsEmpty() {
		u.UID = types.EmptyUID
	}
	u.Update()
}

func (u *User) IsLoggedIn() bool {
	return u.Client != nil && u.Client.Online.Load()
}

func (u *User) tryAutomaticDoublePuppeting() {
	if !u.bridge.Config.CanAutoDoublePuppet(u.MXID) {
		return
	}
	u.log.Debugln("Checking if double puppeting needs to be enabled")
	puppet := u.bridge.GetPuppetByUID(u.UID)
	if len(puppet.CustomMXID) > 0 {
		u.log.Debugln("User already has double-puppeting enabled")
		// Custom puppet already enabled
		return
	}
	accessToken, err := puppet.loginWithSharedSecret(u.MXID)
	if err != nil {
		u.log.Warnln("Failed to login with shared secret:", err)
		return
	}
	err = puppet.SwitchCustomMXID(accessToken, u.MXID)
	if err != nil {
		puppet.log.Warnln("Failed to switch to auto-logined custom puppet:", err)
		return
	}
	u.log.Infoln("Successfully automatically enabled custom puppet")
}

func (u *User) getDirectChats() map[id.UserID][]id.RoomID {
	res := make(map[id.UserID][]id.RoomID)
	privateChats := u.bridge.DB.Portal.FindPrivateChats(u.UID)
	for _, portal := range privateChats {
		if len(portal.MXID) > 0 {
			res[u.bridge.FormatPuppetMXID(portal.Key.UID)] = []id.RoomID{portal.MXID}
		}
	}

	return res
}

func (u *User) UpdateDirectChats(chats map[id.UserID][]id.RoomID) {
	if !u.bridge.Config.Bridge.SyncDirectChatList {
		return
	}
	puppet := u.bridge.GetPuppetByCustomMXID(u.MXID)
	if puppet == nil || puppet.CustomIntent() == nil {
		return
	}
	intent := puppet.CustomIntent()
	method := http.MethodPatch
	if chats == nil {
		chats = u.getDirectChats()
		method = http.MethodPut
	}
	u.log.Debugln("Updating m.direct list on homeserver")
	var err error
	existingChats := make(map[id.UserID][]id.RoomID)
	err = intent.GetAccountData(event.AccountDataDirectChats.Type, &existingChats)
	if err != nil {
		u.log.Warnln("Failed to get m.direct list to update it:", err)
		return
	}
	for userID, rooms := range existingChats {
		if _, ok := u.bridge.ParsePuppetMXID(userID); !ok {
			// This is not a ghost user, include it in the new list
			chats[userID] = rooms
		} else if _, ok := chats[userID]; !ok && method == http.MethodPatch {
			// This is a ghost user, but we're not replacing the whole list, so include it too
			chats[userID] = rooms
		}
	}
	err = intent.SetAccountData(event.AccountDataDirectChats.Type, &chats)
	if err != nil {
		u.log.Warnln("Failed to update m.direct list:", err)
	}
}

func (u *User) GetPortalByUID(uid types.UID) *Portal {
	return u.bridge.GetPortalByUID(database.NewPortalKey(uid, u.UID))
}

func (u *User) ResyncContacts(forceAvatarSync bool) error {
	if err := u.Client.ReloadFriendList(); err != nil {
		return fmt.Errorf("failed to reload contacts: %v", err)
	}
	for _, contact := range u.Client.FriendList {
		uid := types.NewIntUserUID(contact.Uin)
		puppet := u.bridge.GetPuppetByUID(uid)
		if puppet != nil {
			puppet.Sync(u, &types.ContactInfo{Name: contact.Nickname, Remark: contact.Remark}, forceAvatarSync, true)
		} else {
			u.log.Warnfln("Got a nil puppet for %s while syncing contacts", uid)
		}
	}

	return nil
}

func (u *User) ResyncGroups(createPortals bool) error {
	if err := u.Client.ReloadGroupList(); err != nil {
		return fmt.Errorf("failed to reload groups: %v", err)
	}
	for _, group := range u.Client.GroupList {
		uid := types.NewIntGroupUID(group.Code)
		portal := u.GetPortalByUID(uid)
		if len(portal.MXID) == 0 {
			if createPortals {
				if err := portal.CreateMatrixRoom(u, group, true); err != nil {
					return fmt.Errorf("failed to create room for %s: %v", uid, err)
				}
			}
		} else {
			portal.UpdateMatrixRoom(u, group, true)
		}
	}

	return nil
}

func (u *User) StartPM(uid types.UID, reason string) (*Portal, *Puppet, bool, error) {
	u.log.Debugln("Starting PM with", uid, "from", reason)
	puppet := u.bridge.GetPuppetByUID(uid)
	puppet.SyncContact(u, true, reason)
	portal := u.GetPortalByUID(puppet.UID)
	if len(portal.MXID) > 0 {
		ok := portal.ensureUserInvited(u)
		if !ok {
			portal.log.Warnfln("ensureUserInvited(%s) returned false, creating new portal", u.MXID)
			portal.MXID = ""
		} else {
			return portal, puppet, false, nil
		}
	}
	err := portal.CreateMatrixRoom(u, nil, false)

	return portal, puppet, true, err
}

func (u *User) handlePrivateMessage(c *client.QQClient, m *message.PrivateMessage) {
	var key database.PortalKey
	if m.Sender.Uin == u.Client.Uin {
		key = database.NewPortalKey(types.NewIntUserUID(m.Target), types.NewIntUserUID(m.Sender.Uin))
	} else {
		key = database.NewPortalKey(types.NewIntUserUID(m.Sender.Uin), types.NewIntUserUID(m.Target))
	}
	portal := u.bridge.GetPortalByUID(key)
	portal.messages <- PortalMessage{private: m, source: u}
}

func (u *User) handleGroupMessage(c *client.QQClient, m *message.GroupMessage) {
	uid := types.NewIntGroupUID(m.GroupCode)
	portal := u.bridge.GetPortalByUID(database.NewPortalKey(uid, u.UID))
	portal.messages <- PortalMessage{group: m, source: u}
}

func (u *User) handleTempMessage(c *client.QQClient, e *client.TempMessageEvent) {
	uid := types.NewIntUserUID(e.Message.Sender.Uin)
	portal := u.bridge.GetPortalByUID(database.NewPortalKey(uid, u.UID))
	portal.messages <- PortalMessage{temp: e.Message, source: u}
}

func (u *User) handleOfflineFileEvent(c *client.QQClient, e *client.OfflineFileEvent) {
	uid := types.NewIntUserUID(e.Sender)
	portal := u.bridge.GetPortalByUID(database.NewPortalKey(uid, u.UID))
	portal.messages <- PortalMessage{offline: e, source: u}
}

func (u *User) handleGroupJoin(c *client.QQClient, e *client.GroupInvitedRequest) {
	portal := u.GetPortalByUID(types.NewIntGroupUID(e.GroupCode))
	// FIXME: do u.Client.ReloadGroupList() ?
	groupInfo := u.Client.FindGroup(e.GroupCode)
	if groupInfo == nil {
		u.log.Errorln("Failed to fetch group %s(%d)", e.GroupName, e.GroupCode)
	} else {
		m, err := u.Client.GetGroupMembers(groupInfo)
		if err != nil {
			u.log.Errorln("Failed to get group members %s(%d)", e.GroupName, e.GroupCode)
			return
		}
		groupInfo.Members = m
		if len(portal.MXID) == 0 {
			err := portal.CreateMatrixRoom(u, groupInfo, true)
			if err != nil {
				u.log.Errorln("Failed to create Matrix room after join notification: %v", err)
			}
		} else {
			portal.UpdateMatrixRoom(u, groupInfo, true)
		}
	}
}

func (u *User) handleGroupLeave(c *client.QQClient, e *client.GroupLeaveEvent) {
	portal := u.GetPortalByUID(types.NewIntGroupUID(e.Group.Code))
	if portal == nil || len(portal.MXID) == 0 {
		u.log.Debugfln("Ignoring group info update in chat with no portal: %s(%d)", e.Group.Name, e.Group.Code)
		return
	}

	if e.Operator != nil {
		portal.HandleQQGroupMemberKick(u, types.NewIntUserUID(e.Operator.Uin), u.UID)
	} else {
		portal.HandleQQGroupMemberKick(u, types.EmptyUID, u.UID)
	}
}

func (u *User) handleGroupMemberJoin(c *client.QQClient, e *client.MemberJoinGroupEvent) {
	portal := u.GetPortalByUID(types.NewIntGroupUID(e.Group.Code))
	if portal == nil || len(portal.MXID) == 0 {
		u.log.Debugfln("Ignoring group info update in chat with no portal: %s(%d)", e.Group.Name, e.Group.Code)
		return
	}

	portal.HandleQQGroupMemberInvite(u, types.EmptyUID, types.NewIntUserUID(e.Member.Uin))
}

func (u *User) handleGroupMemberLeave(c *client.QQClient, e *client.MemberLeaveGroupEvent) {
	portal := u.GetPortalByUID(types.NewIntGroupUID(e.Group.Code))
	if portal == nil || len(portal.MXID) == 0 {
		u.log.Debugfln("Ignoring group info update in chat with no portal: %s(%d)", e.Group.Name, e.Group.Code)
		return
	}

	if e.Operator != nil {
		portal.HandleQQGroupMemberKick(u, types.NewIntUserUID(e.Operator.Uin), types.NewIntUserUID(e.Member.Uin))
	} else {
		portal.HandleQQGroupMemberKick(u, types.EmptyUID, types.NewIntUserUID(e.Member.Uin))
	}
}

func (u *User) handleGroupMute(c *client.QQClient, e *client.GroupMuteEvent) {
	// TODO:
}

func (u *User) handleGroupRecalled(c *client.QQClient, e *client.GroupMessageRecalledEvent) {
	portal := u.GetPortalByUID(types.NewIntGroupUID(e.GroupCode))
	// FIXME: e.OperatorUin ?
	portal.HandleQQMessageRevoke(u, e.MessageId, int64(e.Time), e.AuthorUin)
}

func (u *User) handleFriendRecalled(c *client.QQClient, e *client.FriendMessageRecalledEvent) {
	key := database.NewPortalKey(types.NewIntUserUID(e.FriendUin), types.NewIntUserUID(u.Client.Uin))
	portal := u.bridge.GetPortalByUID(key)
	portal.HandleQQMessageRevoke(u, e.MessageId, e.Time, e.FriendUin)
}

func (u *User) handleMemberCardUpdated(c *client.QQClient, e *client.MemberCardUpdatedEvent) {
	portal := u.GetPortalByUID(types.NewIntGroupUID(e.Group.Code))
	if portal == nil || len(portal.MXID) == 0 {
		u.log.Debugfln("Ignoring member card update in chat with no portal: %s(%d)", e.Group.Name, e.Group.Code)
		return
	}

	portal.UpdateRoomNickname(e.Member)
}

func (u *User) updateAvatar(uid types.UID, avatarID *string, avatarURL *id.ContentURI, avatarSet *bool, log log.Logger, intent *appservice.IntentAPI) bool {
	var data []byte
	var err error
	if uid.IsUser() {
		data, err = downloadUserAvatar(u, uid.Uin)
	} else {
		data, err = downloadGroupAvatar(uid.Uin)
	}

	if err != nil {
		log.Warnln("Failed to download avatar:", err)
		return false
	}

	md5sum := fmt.Sprintf("%x", md5.Sum(data))
	if md5sum == *avatarID {
		return false
	}

	resp, err := reuploadAvatar(intent, data)
	if err != nil {
		log.Warnln("Failed to reupload avatar:", err)
		return false
	}

	*avatarURL = resp
	*avatarID = md5sum
	*avatarSet = true

	return true
}

func downloadUserAvatar(user *User, uin string) (data []byte, err error) {
	avatarSizes := []int{0, 640, 140, 100, 41, 40}

	for _, size := range avatarSizes {
		url := fmt.Sprintf("https://q.qlogo.cn/headimg_dl?dst_uin=%s&spec=%d", uin, size)
		data, err = GetBytes(url)
		if err != nil || fmt.Sprintf("%x", md5.Sum(data)) == emptyAvatar {
			continue
		} else {
			break
		}
	}
	return
}

func downloadGroupAvatar(uin string) ([]byte, error) {
	url := fmt.Sprintf("https://p.qlogo.cn/gh/%s/%s/0", uin, uin)
	return GetBytes(url)
}

// ChildOverride
func (br *QQBridge) GetIUser(userID id.UserID, create bool) bridge.User {
	return br.getUserByMXID(userID, false)
}

func (br *QQBridge) GetUserByMXID(userID id.UserID) *User {
	return br.getUserByMXID(userID, false)
}

func (br *QQBridge) getUserByMXID(userID id.UserID, onlyIfExists bool) *User {
	_, isPuppet := br.ParsePuppetMXID(userID)
	if isPuppet || userID == br.Bot.UserID {
		return nil
	}

	br.usersLock.Lock()
	defer br.usersLock.Unlock()

	user, ok := br.usersByMXID[userID]
	if !ok {
		userIDPtr := &userID
		if onlyIfExists {
			userIDPtr = nil
		}

		return br.loadDBUser(br.DB.User.GetByMXID(userID), userIDPtr)
	}

	return user
}

func (br *QQBridge) GetUserByMXIDIfExists(userID id.UserID) *User {
	return br.getUserByMXID(userID, true)
}

func (br *QQBridge) GetUserByUID(uid types.UID) *User {
	br.usersLock.Lock()
	defer br.usersLock.Unlock()

	user, ok := br.usersByUsername[uid.Uin]
	if !ok {
		return br.loadDBUser(br.DB.User.GetByUin(uid.Uin), nil)
	}

	return user
}

func (br *QQBridge) GetAllUsers() []*User {
	br.usersLock.Lock()
	defer br.usersLock.Unlock()

	dbUsers := br.DB.User.GetAll()
	output := make([]*User, len(dbUsers))
	for index, dbUser := range dbUsers {
		user, ok := br.usersByMXID[dbUser.MXID]
		if !ok {
			user = br.loadDBUser(dbUser, nil)
		}
		output[index] = user
	}

	return output
}

func (br *QQBridge) loadDBUser(dbUser *database.User, mxid *id.UserID) *User {
	if dbUser == nil {
		if mxid == nil {
			return nil
		}
		dbUser = br.DB.User.New()
		dbUser.MXID = *mxid
		dbUser.Insert()
	}
	user := br.NewUser(dbUser)
	br.usersByMXID[user.MXID] = user
	if !user.UID.IsEmpty() {
		if len(user.Token) == 0 {
			user.log.Warnfln("Didn't find token for %s, treating user as logged out", user.UID)
			user.UID = types.EmptyUID
			user.Update()
		} else {
			br.usersByUsername[user.UID.Uin] = user
		}
	}
	if len(user.ManagementRoom) > 0 {
		br.managementRooms[user.ManagementRoom] = user
	}

	return user
}

func (br *QQBridge) NewUser(dbUser *database.User) *User {
	user := &User{
		User:   dbUser,
		bridge: br,
		log:    br.Log.Sub("User").Sub(string(dbUser.MXID)),

		resyncQueue: make(map[types.UID]resyncQueueItem),
	}

	user.PermissionLevel = user.bridge.Config.Bridge.Permissions.Get(user.MXID)
	user.Whitelisted = user.PermissionLevel >= bridgeconfig.PermissionLevelUser
	user.Admin = user.PermissionLevel >= bridgeconfig.PermissionLevelAdmin
	user.BridgeState = br.NewBridgeStateQueue(user, user.log)

	go user.puppetResyncLoop()

	return user
}

func setClientProtocol(device *client.DeviceInfo, protocol int) {
	switch protocol {
	case 1:
		device.Protocol = client.AndroidPhone
	case 2:
		device.Protocol = client.AndroidWatch
	case 3:
		device.Protocol = client.MacOS
	case 4:
		device.Protocol = client.QiDian
	case 5:
		device.Protocol = client.IPad
	case 6:
		device.Protocol = client.AndroidPad
	default:
		device.Protocol = client.AndroidPad
	}
}

func energy(signServer string, uin uint64, id string, appVersion string, salt []byte) ([]byte, error) {
	if !strings.HasSuffix(signServer, "/") {
		signServer += "/"
	}

	response, err := Request{
		Method: http.MethodGet,
		URL:    signServer + "custom_energy" + fmt.Sprintf("?data=%v&salt=%v", id, hex.EncodeToString(salt)),
	}.Bytes()
	if err != nil {
		return nil, err
	}

	data, err := hex.DecodeString(gjson.GetBytes(response, "data").String())
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("data is empty")
	}

	return data, nil
}

func sign(signServer string, seq uint64, uin string, cmd string, qua string, buff []byte) (sign []byte, extra []byte, token []byte, err error) {
	if !strings.HasSuffix(signServer, "/") {
		signServer += "/"
	}

	response, err := Request{
		Method: http.MethodPost,
		URL:    signServer + "sign",
		Header: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		Body:   bytes.NewReader([]byte(fmt.Sprintf("uin=%v&qua=%s&cmd=%s&seq=%v&buffer=%v", uin, qua, cmd, seq, hex.EncodeToString(buff)))),
	}.Bytes()
	if err != nil {
		return nil, nil, nil, err
	}

	sign, _ = hex.DecodeString(gjson.GetBytes(response, "data.sign").String())
	extra, _ = hex.DecodeString(gjson.GetBytes(response, "data.extra").String())
	token, _ = hex.DecodeString(gjson.GetBytes(response, "data.token").String())

	return sign, extra, token, nil
}
