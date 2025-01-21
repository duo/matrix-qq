package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/duo/matrix-qq/pkg/qqid"

	"github.com/LagrangeDev/LagrangeGo/client"
	"github.com/LagrangeDev/LagrangeGo/client/auth"
	"github.com/LagrangeDev/LagrangeGo/client/packets/wtlogin/qrcodestate"
	"github.com/LagrangeDev/LagrangeGo/utils/crypto"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

const (
	LoginStepQR       = "me.lxduo.qq.login.qr"
	LoginStepComplete = "me.lxduo.qq.login.complete"
)

type QRLogin struct {
	User   *bridgev2.User
	Main   *QQConnector
	Client *client.QQClient
	Log    zerolog.Logger
}

var _ bridgev2.LoginProcessDisplayAndWait = (*QRLogin)(nil)

func (qc *QQConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{{
		Name:        "QR",
		Description: "Scan a QR code to pair the bridge to your QQ client",
		ID:          "qr",
	}}
}

func (qc *QQConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID != "qr" {
		return nil, fmt.Errorf("invalid login flow ID")
	}

	return &QRLogin{
		User: user,
		Main: qc,
		Log: user.Log.With().
			Str("action", "login").
			Stringer("user_id", user.MXID).
			Logger(),
	}, nil
}

func (qr *QRLogin) Cancel() {
	qr.Client.Release()
	qr.Client = nil
}

func (qr *QRLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	qr.Client = newClient(
		qr.Main.Bridge.Log.With().Stringer("user_id", qr.User.MXID).Logger(),
		auth.NewDeviceInfo(int(crypto.RandU32())),
		qr.Main.Config.SignServers,
	)

	_, qrcode, err := qr.Client.FetchQRCode(1, 2, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch QR code: %w", err)
	}

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeDisplayAndWait,
		StepID:       LoginStepQR,
		Instructions: "Scan the QR code on your QQ app to log in",
		DisplayAndWaitParams: &bridgev2.LoginDisplayAndWaitParams{
			Type: bridgev2.LoginDisplayTypeQR,
			Data: qrcode,
		},
	}, nil
}

func (qr *QRLogin) Wait(ctx context.Context) (*bridgev2.LoginStep, error) {
	if qr.Client == nil {
		return nil, fmt.Errorf("login not started")
	}

	for {
		select {
		case <-ctx.Done():
			qr.Client.Disconnect()
			return nil, ctx.Err()
		default:
			if state, err := qr.Client.GetQRCodeResult(); err != nil {
				return nil, fmt.Errorf("failed to get QR code result: %w", err)
			} else {
				switch state {
				case qrcodestate.Canceled:
					return nil, fmt.Errorf("scanning QR code was canceled by the user")
				case qrcodestate.Expired:
					return nil, fmt.Errorf("QR code expired")
				case qrcodestate.WaitingForScan:
				case qrcodestate.WaitingForConfirm:
				case qrcodestate.Confirmed:
					if res, err := qr.Client.QRCodeLogin(); err != nil {
						return nil, fmt.Errorf("failed to login through QR code: %w", err)
					} else {
						if !res.Success {
							return nil, fmt.Errorf("Error code: %d, message: %s", res.Code, res.ErrorMessage)
						}

						uin := qr.Client.Uin
						name := qr.Client.NickName()
						device := qr.Client.Device()
						token, _ := qr.Client.Sig().Marshal()
						qr.Client.Release()

						ul, err := qr.User.NewLogin(ctx, &database.UserLogin{
							ID:         qqid.MakeUserLoginID(fmt.Sprint(uin)),
							RemoteName: name,
							Metadata: &qqid.UserLoginMetadata{
								Device: device,
								Token:  token,
							},
						}, &bridgev2.NewLoginParams{
							DeleteOnConflict: true,
						})
						if err != nil {
							return nil, fmt.Errorf("failed to create user login: %w", err)
						}

						ul.Client.Connect(ul.Log.WithContext(context.Background()))

						return &bridgev2.LoginStep{
							Type:         bridgev2.LoginStepTypeComplete,
							StepID:       LoginStepComplete,
							Instructions: fmt.Sprintf("Successfully logged in as %s", ul.RemoteName),
							CompleteParams: &bridgev2.LoginCompleteParams{
								UserLoginID: ul.ID,
								UserLogin:   ul,
							},
						}, nil
					}
				}
			}
			time.Sleep(time.Second)
		}
	}
}

func newClient(log zerolog.Logger, device *auth.DeviceInfo, signUrls []string) *client.QQClient {
	app := auth.AppList["linux"]["3.2.15-30366"]

	c := client.NewClientEmpty()
	c.UseVersion(app)
	c.AddSignServer(signUrls...)
	c.SetLogger(protocolLogger{log: log.With().Str("protocol", "qq").Logger()})
	c.UseDevice(device)

	return c
}

type protocolLogger struct {
	log zerolog.Logger
}

func (p protocolLogger) Debug(format string, arg ...any) {
	p.log.Debug().Msgf(format, arg...)
}

func (p protocolLogger) Info(format string, arg ...any) {
	p.log.Info().Msgf(format, arg...)
}

func (p protocolLogger) Warning(format string, arg ...any) {
	p.log.Warn().Msgf(format, arg...)
}

func (p protocolLogger) Error(format string, arg ...any) {
	p.log.Error().Msgf(format, arg...)
}

func (p protocolLogger) Dump(data []byte, format string, arg ...any) {
	p.log.Error().Bytes("data", data).Msgf(format, arg...)
}
