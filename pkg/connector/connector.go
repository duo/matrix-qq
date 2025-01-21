package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/duo/matrix-qq/pkg/msgconv"
	"github.com/duo/matrix-qq/pkg/qqid"

	"github.com/LagrangeDev/LagrangeGo/client"
	"github.com/LagrangeDev/LagrangeGo/client/auth"
	"maunium.net/go/mautrix/bridgev2"
)

var (
	_ bridgev2.NetworkConnector      = (*QQConnector)(nil)
	_ bridgev2.MaxFileSizeingNetwork = (*QQConnector)(nil)
	_ bridgev2.StoppableNetwork      = (*QQConnector)(nil)
)

type QQConnector struct {
	Bridge  *bridgev2.Bridge
	Config  Config
	MsgConv *msgconv.MessageConverter
}

func (qc *QQConnector) Init(bridge *bridgev2.Bridge) {
	qc.Bridge = bridge
	qc.MsgConv = msgconv.NewMessageConverter(bridge)
}

func (qc *QQConnector) Start(ctx context.Context) error {
	return nil
}

func (qc *QQConnector) Stop() {
}

func (qc *QQConnector) SetMaxFileSize(maxSize int64) {
	qc.MsgConv.MaxFileSize = maxSize
}

func (qc *QQConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:      "Matrix QQ",
		NetworkURL:       "https://github.com/duo/matrix-qq",
		NetworkIcon:      "mxc://matrix.org/nKrjlWVnjIGQRJicsBqDFLnc",
		NetworkID:        "qq",
		BeeperBridgeType: "github.com/duo/matrix-qq",
		DefaultPort:      17777,
	}
}

func (qc *QQConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	q := &QQClient{
		Main:        qc,
		UserLogin:   login,
		resyncQueue: make(map[string]resyncQueueItem),
	}
	login.Client = q

	loginMetadata := login.Metadata.(*qqid.UserLoginMetadata)
	if loginMetadata.Device == nil || len(loginMetadata.Token) == 0 {
		return nil
	}

	log := qc.Bridge.Log.With().Stringer("user_id", login.UserMXID).Logger()
	q.Client = newClient(
		log,
		loginMetadata.Device,
		qc.Config.SignServers,
	)

	sig, err := auth.UnmarshalSigInfo(loginMetadata.Token, true)
	if err != nil {
		return fmt.Errorf("failed to unmarshal signature info: %w", err)
	}
	q.Client.UseSig(sig)

	q.Client.DisconnectedEvent.Subscribe(func(cli *client.QQClient, evt *client.DisconnectedEvent) {
		log := log.With().Uint32("qq", cli.Uin).Logger()
		interval := qc.Config.Reconnect.Interval
		maxTimes := qc.Config.Reconnect.MaxTimes
		var times uint = 1

		if cli.Online.Load() {
			return
		}

		log.Warn().Msgf("Offline: %s", evt.Message)
		time.Sleep(time.Second * time.Duration(qc.Config.Reconnect.Delay))
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if times > maxTimes && maxTimes != 0 {
					log.Warn().Msg("Reconnect attempts exceeded the limit, stopping")
					return
				}
				times++

				if interval > 0 {
					log.Warn().Msgf("Attempt to reconnect in %d seconds (%d/%d)", interval, times, maxTimes)
					time.Sleep(time.Second * time.Duration(interval))
				} else {
					time.Sleep(time.Second)
				}

				if cli.Online.Load() {
					log.Info().Msg("Login successful")
					return
				}
				if err := cli.FastLogin(); err == nil {
					log.Info().Msg("Login successful")

					token, _ := cli.Sig().Marshal()
					login.Metadata = &qqid.UserLoginMetadata{
						Device: cli.Device(),
						Token:  token,
					}
					login.Save(ctx)

					return
				} else {
					log.Warn().Err(err).Msgf("Failed to reconnect")
				}
			}
		}
	})

	if err := q.Client.FastLogin(); err != nil {
		return err
	} else {
		token, _ := q.Client.Sig().Marshal()
		login.Metadata = &qqid.UserLoginMetadata{
			Device: q.Client.Device(),
			Token:  token,
		}
		login.Save(ctx)

		return nil
	}
}
