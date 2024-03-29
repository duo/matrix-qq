package internal

import (
	"encoding/json"
	"sync/atomic"
	"time"

	"maunium.net/go/maulogger/v2"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/id"
)

const DefaultSyncProxyBackoff = 1 * time.Second
const MaxSyncProxyBackoff = 60 * time.Second

const BridgeStatusConnected = "CONNECTED"

type WebsocketCommandHandler struct {
	bridge      *QQBridge
	log         maulogger.Logger
	errorTxnIDC *appservice.TransactionIDCache

	lastSyncProxyError time.Time
	syncProxyBackoff   time.Duration
	syncProxyWaiting   int64
}

type BridgeStatus struct {
	StateEvent string    `json:"state_event"`
	Timestamp  int64     `json:"timestamp"`
	TTL        int       `json:"ttl"`
	Source     string    `json:"source"`
	Error      string    `json:"error,omitempty"`
	Message    string    `json:"message,omitempty"`
	UserID     id.UserID `json:"user_id,omitempty"`
	RemoteID   string    `json:"remote_id,omitempty"`
	RemoteName string    `json:"remote_name,omitempty"`

	Info map[string]interface{} `json:"info,omitempty"`
}

func NewWebsocketCommandHandler(br *QQBridge) *WebsocketCommandHandler {
	handler := &WebsocketCommandHandler{
		bridge:           br,
		log:              br.Log.Sub("MatrixWebsocket"),
		errorTxnIDC:      appservice.NewTransactionIDCache(8),
		syncProxyBackoff: DefaultSyncProxyBackoff,
	}
	br.AS.PrepareWebsocket()
	br.AS.SetWebsocketCommandHandler("ping", handler.handleWSPing)
	br.AS.SetWebsocketCommandHandler("syncproxy_error", handler.handleWSSyncProxyError)
	return handler
}

func (mx *WebsocketCommandHandler) handleWSPing(cmd appservice.WebsocketCommand) (bool, interface{}) {
	mx.log.Warnfln("Receive ws ping")
	status := BridgeStatus{
		StateEvent: BridgeStatusConnected,
		Timestamp:  time.Now().Unix(),
		TTL:        600,
		Source:     "bridge",
	}

	return true, &status
}

func (mx *WebsocketCommandHandler) handleWSSyncProxyError(cmd appservice.WebsocketCommand) (bool, interface{}) {
	var data mautrix.RespError
	err := json.Unmarshal(cmd.Data, &data)

	if err != nil {
		mx.log.Warnln("Failed to unmarshal syncproxy_error data:", err)
	} else if txnID, ok := data.ExtraData["txn_id"].(string); !ok {
		mx.log.Warnln("Got syncproxy_error data with no transaction ID")
	} else if mx.errorTxnIDC.IsProcessed(txnID) {
		mx.log.Debugln("Ignoring syncproxy_error with duplicate transaction ID", txnID)
	} else {
		go mx.HandleSyncProxyError(&data, nil)
		mx.errorTxnIDC.MarkProcessed(txnID)
	}

	return true, &data
}

func (mx *WebsocketCommandHandler) HandleSyncProxyError(syncErr *mautrix.RespError, startErr error) {
	if !atomic.CompareAndSwapInt64(&mx.syncProxyWaiting, 0, 1) {
		var err interface{} = startErr
		if err == nil {
			err = syncErr.Err
		}
		mx.log.Debugfln("Got sync proxy error (%v), but there's already another thread waiting to restart sync proxy", err)
		return
	}
	if time.Since(mx.lastSyncProxyError) < MaxSyncProxyBackoff {
		mx.syncProxyBackoff *= 2
		if mx.syncProxyBackoff > MaxSyncProxyBackoff {
			mx.syncProxyBackoff = MaxSyncProxyBackoff
		}
	} else {
		mx.syncProxyBackoff = DefaultSyncProxyBackoff
	}
	mx.lastSyncProxyError = time.Now()
	if syncErr != nil {
		mx.log.Errorfln("Syncproxy told us that syncing failed: %s - Requesting a restart in %s", syncErr.Err, mx.syncProxyBackoff)
	} else if startErr != nil {
		mx.log.Errorfln("Failed to request sync proxy to start syncing: %v - Requesting a restart in %s", startErr, mx.syncProxyBackoff)
	}
	time.Sleep(mx.syncProxyBackoff)
	atomic.StoreInt64(&mx.syncProxyWaiting, 0)
	mx.bridge.RequestStartSync()
}
