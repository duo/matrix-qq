package connector

import (
	"context"
	"time"

	"go.mau.fi/util/ffmpeg"
	"go.mau.fi/util/jsontime"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

const MaxTextLength = 4096
const MaxFileSize = 128 * 1024 * 1024
const MaxImageSize = 128 * 1024 * 1024

func supportedIfFFmpeg() event.CapabilitySupportLevel {
	if ffmpeg.Supported() {
		return event.CapLevelPartialSupport
	}
	return event.CapLevelRejected
}

func catpID() string {
	base := "me.lxduo.qq.capabilities.2025_01_16"
	if ffmpeg.Supported() {
		return base + "+ffmpeg"
	}
	return base
}

var qqCaps = &event.RoomFeatures{
	ID: catpID(),

	Formatting: event.FormattingFeatureMap{
		event.FmtUserLink: event.CapLevelFullySupported,
	},

	File: map[event.CapabilityMsgType]*event.FileFeatures{
		event.MsgImage: {
			MimeTypes: map[string]event.CapabilitySupportLevel{
				"image/png":  event.CapLevelFullySupported,
				"image/jpeg": event.CapLevelFullySupported,
				"image/webp": event.CapLevelFullySupported,
				"image/gif":  event.CapLevelFullySupported,
			},
			Caption:          event.CapLevelDropped,
			MaxCaptionLength: MaxTextLength,
			MaxSize:          MaxImageSize,
		},
		event.MsgAudio: {
			MimeTypes: map[string]event.CapabilitySupportLevel{
				"audio/mpeg": event.CapLevelFullySupported,
				"audio/mp4":  event.CapLevelFullySupported,
				"audio/ogg":  event.CapLevelFullySupported,
				"audio/aac":  event.CapLevelFullySupported,
				"audio/amr":  event.CapLevelFullySupported,
			},
			Caption: event.CapLevelDropped,
			MaxSize: MaxFileSize,
		},
		event.CapMsgVoice: {
			MimeTypes: map[string]event.CapabilitySupportLevel{
				"audio/ogg; codecs=opus": event.CapLevelFullySupported,
				"audio/ogg":              event.CapLevelFullySupported,
			},
			Caption: event.CapLevelDropped,
			MaxSize: MaxFileSize,
		},
		event.CapMsgSticker: {
			MimeTypes: map[string]event.CapabilitySupportLevel{
				"image/webp": event.CapLevelFullySupported,
				"image/png":  event.CapLevelFullySupported,
				"image/jpeg": event.CapLevelFullySupported,
			},
			Caption: event.CapLevelDropped,
			MaxSize: MaxImageSize,
		},
		event.CapMsgGIF: {
			MimeTypes: map[string]event.CapabilitySupportLevel{
				"video/mp4": event.CapLevelFullySupported,
				"image/gif": event.CapLevelFullySupported,
			},
			Caption:          event.CapLevelFullySupported,
			MaxCaptionLength: MaxTextLength,
			MaxSize:          MaxImageSize,
		},
		event.MsgVideo: {
			MimeTypes: map[string]event.CapabilitySupportLevel{
				"video/mp4":  event.CapLevelFullySupported,
				"video/3gpp": event.CapLevelFullySupported,
				"video/webm": supportedIfFFmpeg(),
			},
			Caption:          event.CapLevelDropped,
			MaxCaptionLength: MaxTextLength,
			MaxSize:          MaxFileSize,
		},
		event.MsgFile: {
			MimeTypes: map[string]event.CapabilitySupportLevel{
				"*/*": event.CapLevelFullySupported,
			},
			Caption:          event.CapLevelDropped,
			MaxCaptionLength: MaxTextLength,
			MaxSize:          MaxFileSize,
		},
	},

	MaxTextLength:   MaxTextLength,
	LocationMessage: event.CapLevelFullySupported,
	Reply:           event.CapLevelFullySupported,
	Delete:          event.CapLevelFullySupported,
	DeleteForMe:     false,
	DeleteMaxAge:    ptr.Ptr(jsontime.S(2 * time.Minute)),
}

func (qc *QQClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	return qqCaps
}

func (qc *QQConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{}
}

func (qc *QQConnector) GetBridgeInfoVersion() (info, caps int) {
	return 1, 1
}
