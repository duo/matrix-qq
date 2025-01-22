package qqid

import (
	"compress/gzip"
	"crypto/md5"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
)

const (
	defaultAvatar = "bad9cbb852b22fe58e62f3f23c7d63d2"
)

var (
	avatarSizes = []int{0, 640, 140, 100, 41, 40}
	lruCache    *lru.Cache[uint32, string]
	once        sync.Once

	tlsCipherSuites = []uint16{
		// AEADs w/ ECDHE
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305, tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,

		// CBC w/ ECDHE
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA, tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA, tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,

		// AEADs w/o ECDHE
		tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_RSA_WITH_AES_256_GCM_SHA384,

		// CBC w/o ECDHE
		tls.TLS_RSA_WITH_AES_128_CBC_SHA,
		tls.TLS_RSA_WITH_AES_256_CBC_SHA,

		// 3DES
		tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
		tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
	}

	httpClient = &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2:   true,
			MaxConnsPerHost:     0,
			MaxIdleConns:        0,
			MaxIdleConnsPerHost: 256,
			TLSClientConfig: &tls.Config{
				CipherSuites:       tlsCipherSuites,
				MinVersion:         tls.VersionTLS10,
				InsecureSkipVerify: true,
			},
		},
	}

	UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/87.0.4280.88 Safari/537.36 Edg/87.0.664.66"

	SmallestImg = []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46,
		0x49, 0x46, 0x00, 0x01, 0x01, 0x01, 0x00, 0x48,
		0x00, 0x48, 0x00, 0x00, 0xFF, 0xDB, 0x00, 0x43,
		0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xC2, 0x00, 0x0B, 0x08, 0x00, 0x01,
		0x00, 0x01, 0x01, 0x01, 0x11, 0x00, 0xFF, 0xC4,
		0x00, 0x14, 0x10, 0x01, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0xFF, 0xDA, 0x00, 0x08,
		0x01, 0x01, 0x00, 0x01, 0x3F, 0x10,
	}
)

func GetBytes(url string) ([]byte, error) {
	reader, err := HTTPGetReadCloser(url)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = reader.Close()
	}()

	return io.ReadAll(reader)
}

type gzipCloser struct {
	f io.Closer
	r *gzip.Reader
}

func NewGzipReadCloser(reader io.ReadCloser) (io.ReadCloser, error) {
	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return nil, err
	}

	return &gzipCloser{
		f: reader,
		r: gzipReader,
	}, nil
}

func (g *gzipCloser) Read(p []byte) (n int, err error) {
	return g.r.Read(p)
}

func (g *gzipCloser) Close() error {
	_ = g.f.Close()

	return g.r.Close()
}

func HTTPGetReadCloser(url string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header["User-Agent"] = []string{UserAgent}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
		return NewGzipReadCloser(resp.Body)
	}

	return resp.Body, err
}

func GetUserAvatarURL(uin uint32) string {
	if url, ok := getAvatarCache().Get(uin); ok {
		return url
	}

	url := ""
	for _, size := range avatarSizes {
		url = fmt.Sprintf("https://q.qlogo.cn/headimg_dl?dst_uin=%d&spec=%d", uin, size)
		data, err := GetBytes(url)
		if err != nil || fmt.Sprintf("%x", md5.Sum(data)) == defaultAvatar {
			continue
		} else {
			break
		}
	}
	getAvatarCache().Add(uin, url)

	return url
}

func GetGroupAvatarURL(groupId uint32) string {
	return fmt.Sprintf("https://p.qlogo.cn/gh/%d/%d/0", groupId, groupId)
}

func getAvatarCache() *lru.Cache[uint32, string] {
	once.Do(func() {
		lruCache, _ = lru.New[uint32, string](1024)
	})
	return lruCache
}
