package connector

import (
	"cmp"
	"context"
	"fmt"
	"math/rand/v2"
	"net/url"
	"path"
	"strconv"
	"time"

	"github.com/duo/matrix-qq/pkg/qqid"

	"github.com/LagrangeDev/LagrangeGo/client/entity"
	"github.com/rs/zerolog"
	"go.mau.fi/util/jsontime"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
)

const (
	PrivateChatTopic = "QQ private chat"

	powerDefault    = 0
	powerAdmin      = 50
	powerSuperAdmin = 75

	resyncMinInterval  = 7 * 24 * time.Hour
	resyncLoopInterval = 4 * time.Hour
)

func (qc *QQClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	meta := portal.Metadata.(*qqid.PortalMetadata)
	portalID := string(portal.ID)

	switch meta.ChatType {
	case qqid.ChatPrivate:
		return qc.getDirectChatInfo(portalID)
	case qqid.ChatGroup:
		return qc.getGroupChatInfo(ctx, portal)
	case qqid.ChatTemp:
		return nil, fmt.Errorf("temporary chat not supported")
	}

	return nil, fmt.Errorf("unknown chat type")
}

func (qc *QQClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	if ghost.Name != "" {
		qc.EnqueueGhostResync(ghost)
		return nil, nil
	}

	id, _ := strconv.ParseUint(string(ghost.ID), 10, 32)

	if info, err := qc.Client.FetchUserInfoUin(uint32(id)); err != nil {
		return nil, fmt.Errorf("failed to fetch user #%d info", id)
	} else {
		return qc.contactToUserInfo(info), nil
	}
}

func (qc *QQClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	ghost, err := qc.Main.Bridge.GetGhostByID(ctx, qqid.MakeUserID(identifier))
	if err != nil {
		return nil, fmt.Errorf("failed to get ghost: %w", err)
	}

	return &bridgev2.ResolveIdentifierResponse{
		Ghost:  ghost,
		UserID: qqid.MakeUserID(identifier),
		Chat:   &bridgev2.CreateChatResponse{PortalKey: qc.makeDMPortalKey(identifier)},
	}, nil
}

func (qc *QQClient) getDirectChatInfo(recipient string) (*bridgev2.ChatInfo, error) {
	members := &bridgev2.ChatMemberList{
		IsFull:           true,
		TotalMemberCount: 2,
		OtherUserID:      qqid.MakeUserID(recipient),
		PowerLevels:      nil,
	}

	if networkid.UserLoginID(recipient) != qc.UserLogin.ID {
		selfEvtSender := qc.selfEventSender()
		members.MemberMap = map[networkid.UserID]bridgev2.ChatMember{
			selfEvtSender.Sender: {EventSender: selfEvtSender},
			members.OtherUserID:  {EventSender: qc.makeEventSender(recipient)},
		}
	} else {
		members.MemberMap = map[networkid.UserID]bridgev2.ChatMember{
			// For chats with self, force-split the members so the user's own ghost is always in the room.
			"":                  {EventSender: bridgev2.EventSender{IsFromMe: true}},
			members.OtherUserID: {EventSender: bridgev2.EventSender{Sender: members.OtherUserID}},
		}
	}

	return &bridgev2.ChatInfo{
		Topic:        ptr.Ptr(PrivateChatTopic),
		Members:      members,
		Type:         ptr.Ptr(database.RoomTypeDM),
		ExtraUpdates: updateChatType(qqid.ChatPrivate),
	}, nil
}

func (qc *QQClient) getGroupChatInfo(_ context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	uin, _ := strconv.ParseUint(string(portal.ID), 10, 32)

	groupInfo := qc.Client.GetCachedGroupInfo(uint32(uin))
	membersInfo := qc.Client.GetCachedMembersInfo(uint32(uin))
	if groupInfo == nil || membersInfo == nil {
		return nil, fmt.Errorf("failed to fetch group info")
	}

	wrapped := &bridgev2.ChatInfo{
		Name:   ptr.Ptr(groupInfo.GroupName),
		Avatar: wrapAvatar(qqid.GetGroupAvatarURL(groupInfo.GroupUin)),
		Members: &bridgev2.ChatMemberList{
			IsFull:           true,
			TotalMemberCount: len(membersInfo),
			MemberMap:        make(map[networkid.UserID]bridgev2.ChatMember, len(membersInfo)),
			PowerLevels: &bridgev2.PowerLevelOverrides{
				Events: map[event.Type]int{
					event.StateRoomName:   powerDefault,
					event.StateRoomAvatar: powerDefault,
					event.StateTopic:      powerDefault,
					event.EventReaction:   powerDefault,
					event.EventRedaction:  powerDefault,
				},
				EventsDefault: ptr.Ptr(powerDefault),
				StateDefault:  ptr.Ptr(powerAdmin),
			},
		},
		Disappear:    &database.DisappearingSetting{Type: database.DisappearingTypeNone},
		Type:         ptr.Ptr(database.RoomTypeDefault),
		ExtraUpdates: updateChatType(qqid.ChatGroup),
	}

	for _, m := range membersInfo {
		evtSender := qc.makeEventSender(fmt.Sprint(m.Uin))
		pl := powerDefault
		if m.Permission == entity.Owner {
			pl = powerSuperAdmin
		} else if m.Permission == entity.Admin {
			pl = powerAdmin
		}

		wrapped.Members.MemberMap[evtSender.Sender] = bridgev2.ChatMember{
			EventSender: evtSender,
			Membership:  event.MembershipJoin,
			PowerLevel:  &pl,
		}
	}

	return wrapped, nil
}

func (qc *QQClient) contactToUserInfo(contact *entity.User) *bridgev2.UserInfo {
	return &bridgev2.UserInfo{
		IsBot:        nil,
		Identifiers:  []string{},
		ExtraUpdates: updateGhostLastSyncAt,
		Name: ptr.Ptr(qc.Main.Config.FormatDisplayname(DisplaynameParams{
			Alias: contact.Remarks,
			Name:  contact.Nickname,
			ID:    fmt.Sprint(contact.Uin),
		})),
		Avatar: wrapAvatar(contact.Avatar),
	}
}

func (qc *QQClient) EnqueueGhostResync(ghost *bridgev2.Ghost) {
	if ghost.Metadata.(*qqid.GhostMetadata).LastSync.Add(resyncMinInterval).After(time.Now()) {
		return
	}

	qc.resyncQueueLock.Lock()
	uid := fmt.Sprintf("u\u0001%s", string(ghost.ID))
	if _, exists := qc.resyncQueue[uid]; !exists {
		qc.resyncQueue[uid] = resyncQueueItem{ghost: ghost}
		qc.UserLogin.Log.Debug().
			Str("uid", uid).
			Stringer("next_resync_in", time.Until(qc.nextResync)).
			Msg("Enqueued resync for ghost")
	}
	qc.resyncQueueLock.Unlock()
}

func (qc *QQClient) EnqueuePortalResync(portal *bridgev2.Portal) {
	meta := portal.Metadata.(*qqid.PortalMetadata)
	if meta.ChatType != qqid.ChatGroup || meta.LastSync.Add(resyncMinInterval).After(time.Now()) {
		return
	}

	qc.resyncQueueLock.Lock()
	gid := fmt.Sprintf("g\u0001%s", string(portal.ID))
	if _, exists := qc.resyncQueue[gid]; !exists {
		qc.resyncQueue[gid] = resyncQueueItem{portal: portal}
		qc.UserLogin.Log.Debug().
			Str("gid", gid).
			Stringer("next_resync_in", time.Until(qc.nextResync)).
			Msg("Enqueued resync for portal")
	}
	qc.resyncQueueLock.Unlock()
}

func (qc *QQClient) ghostResyncLoop(ctx context.Context) {
	log := qc.UserLogin.Log.With().Str("action", "ghost resync loop").Logger()
	ctx = log.WithContext(ctx)
	qc.nextResync = time.Now().Add(resyncLoopInterval).Add(-time.Duration(rand.IntN(3600)) * time.Second)
	timer := time.NewTimer(time.Until(qc.nextResync))
	log.Info().Time("first_resync", qc.nextResync).Msg("Ghost resync queue starting")

	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		queue := qc.rotateResyncQueue()
		timer.Reset(time.Until(qc.nextResync))
		if len(queue) > 0 {
			qc.doGhostResync(ctx, queue)
		} else {
			log.Trace().Msg("Nothing in background resync queue")
		}
	}
}

func (qc *QQClient) rotateResyncQueue() map[string]resyncQueueItem {
	qc.resyncQueueLock.Lock()
	defer qc.resyncQueueLock.Unlock()
	qc.nextResync = time.Now().Add(resyncLoopInterval)
	if len(qc.resyncQueue) == 0 {
		return nil
	}
	queue := qc.resyncQueue
	qc.resyncQueue = make(map[string]resyncQueueItem)
	return queue
}

func (qc *QQClient) doGhostResync(ctx context.Context, queue map[string]resyncQueueItem) {
	log := zerolog.Ctx(ctx)
	if !qc.IsLoggedIn() {
		log.Warn().Msg("Not logged in, skipping background resyncs")
		return
	}

	log.Debug().Msg("Starting background resyncs")
	defer log.Debug().Msg("Background resyncs finished")

	qc.Client.RefreshFriendCache()
	qc.Client.RefreshAllGroupsInfo()
	qc.Client.RefreshAllGroupMembersCache()

	var ghosts []*bridgev2.Ghost
	var portals []*bridgev2.Portal

	for id, item := range queue {
		var key string
		var lastSync time.Time
		if item.ghost != nil {
			key = "uid"
			lastSync = item.ghost.Metadata.(*qqid.GhostMetadata).LastSync.Time
		} else if item.portal != nil {
			key = "gid"
			lastSync = item.portal.Metadata.(*qqid.PortalMetadata).LastSync.Time
		}

		if lastSync.Add(resyncMinInterval).After(time.Now()) {
			log.Debug().
				Str(key, id).
				Time("last_sync", lastSync).
				Msg("Not resyncing, last sync was too recent")
			continue
		}

		if item.ghost != nil {
			ghosts = append(ghosts, item.ghost)
		} else if item.portal != nil {
			portals = append(portals, item.portal)
		}
	}

	for _, portal := range portals {
		qc.Main.Bridge.QueueRemoteEvent(qc.UserLogin, &simplevent.ChatResync{
			EventMeta: simplevent.EventMeta{
				Type: bridgev2.RemoteEventChatResync,
				LogContext: func(c zerolog.Context) zerolog.Context {
					return c.Str("sync_reason", "queue")
				},
				PortalKey: portal.PortalKey,
			},
			GetChatInfoFunc: func(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
				info, err := qc.GetChatInfo(ctx, portal)
				if err == nil {
					info.ExtraUpdates = bridgev2.MergeExtraUpdaters(
						info.ExtraUpdates,
						qc.updateMemberDisplyname,
					)
				}
				return info, err
			},
		})
	}

	for _, ghost := range ghosts {
		id, _ := strconv.ParseUint(string(ghost.ID), 10, 32)
		contact, err := qc.Client.FetchUserInfoUin(uint32(id))
		if err != nil {
			log.Warn().Uint64("id", id).Msg("Failed to get user info for puppet in background sync")
			continue
		}

		ghost.UpdateInfo(ctx, qc.contactToUserInfo(contact))
	}
}

func (qc *QQClient) updateMemberDisplyname(ctx context.Context, portal *bridgev2.Portal) bool {
	groupID, _ := strconv.ParseUint(string(portal.ID), 10, 32)
	if members := qc.Client.GetCachedMembersInfo(uint32(groupID)); members != nil {
		for _, member := range members {
			memberIntent := portal.GetIntentFor(ctx, qc.makeEventSender(fmt.Sprint(member.Uin)), qc.UserLogin, bridgev2.RemoteEventChatInfoChange)

			mxid := memberIntent.GetMXID()

			memberInfo, err := portal.Bridge.Matrix.GetMemberInfo(ctx, portal.MXID, mxid)
			if err != nil {
				zerolog.Ctx(ctx).Err(err).Msg("Failed to get member info")
				continue
			}

			displayName := cmp.Or(member.Remarks, member.MemberCard, member.Nickname)
			if memberInfo.Displayname != displayName {
				memberInfo.Displayname = displayName

				var zeroTime time.Time
				_, err = memberIntent.SendState(ctx, portal.MXID, event.StateMember, mxid.String(), &event.Content{
					Parsed: memberInfo,
				}, zeroTime)

				if err != nil {
					zerolog.Ctx(ctx).Err(err).Stringer("user_id", mxid).Msg("Failed to update group displayname")
				}
				zerolog.Ctx(ctx).Debug().Stringer("user_id", mxid).Msgf("Update group displayname to %s", displayName)
			}
		}
	}

	return false
}

func updateChatType(chatType qqid.ChatType) func(context.Context, *bridgev2.Portal) bool {
	return func(ctx context.Context, portal *bridgev2.Portal) (changed bool) {
		meta := portal.Metadata.(*qqid.PortalMetadata)
		if meta.ChatType != chatType {
			meta.ChatType = chatType
			changed = true
		}

		return
	}
}

func updateGhostLastSyncAt(ctx context.Context, ghost *bridgev2.Ghost) bool {
	meta := ghost.Metadata.(*qqid.GhostMetadata)
	forceSave := time.Since(meta.LastSync.Time) > 24*time.Hour
	meta.LastSync = jsontime.UnixNow()
	return forceSave
}

func wrapAvatar(avatarURL string) *bridgev2.Avatar {
	if avatarURL == "" {
		return &bridgev2.Avatar{Remove: true}
	}
	parsedURL, _ := url.Parse(avatarURL)
	avatarID := path.Base(parsedURL.Path)
	return &bridgev2.Avatar{
		ID: networkid.AvatarID(avatarID),
		Get: func(ctx context.Context) ([]byte, error) {
			return qqid.GetBytes(avatarURL)
		},
	}
}
