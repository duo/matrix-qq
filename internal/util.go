package internal

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"math/rand"
	"net/http"
	"os/exec"
	"strings"

	"github.com/wdvxdr1123/go-silk"
)

var (
	httpClient = &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2:   true,
			MaxConnsPerHost:     0,
			MaxIdleConns:        0,
			MaxIdleConnsPerHost: 256,
		},
	}

	UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/87.0.4280.88 Safari/537.36 Edg/87.0.664.66"

	smallestImg = []byte{
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

	// created by JogleLew and jqqqqqqqqqq, optimized based on Tim's emoji support, updated by xzsk2 to mobileqq v8.8.11
	emojis = map[string]string{
		"NO": "🚫",
		"OK": "👌",
		"不开心": "😞",
		"乒乓": "🏓",
		"便便": "💩",
		"偷笑": "😏",
		"傲慢": "😕",
		"再见": "👋",
		"冷汗": "😅",
		"凋谢": "🥀",
		"刀": "🔪",
		"发呆": "😳",
		"发怒": "😡",
		"发抖": "😮",
		"可爱": "😊",
		"右哼哼": "😏",
		"吐": "😨",
		"吓": "🙀",
		"呲牙": "😃",
		"咒骂": "😤",
		"咖啡": "☕️",
		"哈欠": "🥱",
		"啤酒": "🍺",
		"啵啵": "😙",
		"喝奶": "🍼",
		"喝彩": "👏",
		"嘘": "🤐",
		"困": "😪",
		"坏笑": "😏",
		"大哭": "😭",
		"大笑": "😄",
		"太阳": "🌞️",
		"奋斗": "✊",
		"好棒": "👍",
		"委屈": "😭",
		"害怕": "😨",
		"害羞": "☺️",
		"尴尬": "😰",
		"左亲亲": "😚",
		"左哼哼": "😏",
		"干杯": "🍻",
		"幽灵": "👻",
		"开枪": "🔫",
		"得意": "😎",
		"微笑": "🙂",
		"心碎": "💔️",
		"快哭了": "😭",
		"悠闲": "🤑",
		"惊呆": "😮",
		"惊恐": "😨",
		"惊讶": "😮",
		"憨笑": "😬",
		"手枪": "🔫",
		"抓狂": "😤",
		"折磨": "😩",
		"抱抱": "🤗",
		"拍手": "👏",
		"拜托": "👋",
		"拥抱": "🤷",
		"拳头": "✊",
		"挥手": "👋",
		"握手": "🤝",
		"撇嘴": "😣",
		"敲打": "🔨",
		"晕": "😵",
		"月亮": "🌃",
		"棒棒糖": "🍭",
		"河蟹": "🦀",
		"泪奔": "😭",
		"流汗": "😓",
		"流泪": "😭",
		"灯笼": "🏮",
		"炸弹": "💣",
		"点赞": "👍",
		"爱你": "🤟",
		"爱心": "❤️",
		"爱情": "💑",
		"猪头": "🐷",
		"献吻": "😘",
		"玫瑰": "🌹",
		"瓢虫": "🐞",
		"生日快乐": "🎂",
		"疑问": "🤔",
		"白眼": "🙄",
		"睡": "😴",
		"示爱": "❤️",
		"礼物": "🎁",
		"祈祷": "🙏",
		"笑哭": "😂",
		"篮球": "🏀",
		"红包": "🧧",
		"胜利": "✌️",
		"色": "😍",
		"茶": "🍵",
		"药": "💊",
		"菊花": "🌼",
		"菜刀": "🔪",
		"蛋": "🥚",
		"蛋糕": "🎂",
		"衰": "💣",
		"西瓜": "🍉",
		"调皮": "😝",
		"赞": "👍",
		"足球": "⚽️",
		"跳跳": "🕺",
		"踩": "👎",
		"送花": "💐",
		"酷": "🤓",
		"钞票": "💵",
		"闪电": "⚡",
		"闭嘴": "😷",
		"难过": "🙁",
		"鞭炮": "🧨",
		"飙泪": "😭",
		"飞吻": "🥰",
		"飞机": "🛩",
		"饥饿": "🤤",
		"饭": "🍚",
		"骷髅": "💀",
		"鼓掌": "👏",
	}
)

const sampleRate = 24000
const numChannels = 1
const precision = 2

type waveHeader struct {
	RiffMark      [4]byte
	FileSize      int32
	WaveMark      [4]byte
	FmtMark       [4]byte
	FormatSize    int32
	FormatType    int16
	NumChans      int16
	SampleRate    int32
	ByteRate      int32
	BytesPerFrame int16
	BitsPerSample int16
	DataMark      [4]byte
	DataSize      int32
}

func convertToOgg(rawData []byte) ([]byte, error) {
	pcmData, err := silk.DecodeSilkBuffToPcm(rawData, sampleRate)
	if err != nil {
		return nil, err
	}

	header := waveHeader{
		RiffMark:      [4]byte{'R', 'I', 'F', 'F'},
		FileSize:      int32(44 + len(pcmData)),
		WaveMark:      [4]byte{'W', 'A', 'V', 'E'},
		FmtMark:       [4]byte{'f', 'm', 't', ' '},
		FormatSize:    16,
		FormatType:    1,
		NumChans:      int16(numChannels),
		SampleRate:    int32(sampleRate),
		ByteRate:      int32(sampleRate * numChannels * precision),
		BytesPerFrame: int16(numChannels * precision),
		BitsPerSample: int16(precision) * 8,
		DataMark:      [4]byte{'d', 'a', 't', 'a'},
		DataSize:      int32(len(pcmData)),
	}

	buf := &bytes.Buffer{}
	if err := binary.Write(buf, binary.LittleEndian, &header); err != nil {
		return nil, err
	}
	if _, err := buf.Write(pcmData); err != nil {
		return nil, err
	}

	cmd := exec.Command(
		"ffmpeg", "-i", "pipe:0", "-c:a", "libopus", "-b:a", "24K", "-f", "ogg", "pipe:1",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	io.Copy(stdin, buf)
	stdin.Close()

	outputBuf := &bytes.Buffer{}
	io.Copy(outputBuf, stdout)

	if err := cmd.Wait(); err != nil {
		return nil, err
	}

	return outputBuf.Bytes(), nil
}

func convertToSilk(rawData []byte) ([]byte, error) {
	buf := bytes.NewBuffer(rawData)

	cmd := exec.Command(
		"ffmpeg", "-i", "pipe:0", "-f", "s16le", "-ar", "24000", "-ac", "1", "pipe:1",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	io.Copy(stdin, buf)
	stdin.Close()

	waveBuf := &bytes.Buffer{}
	io.Copy(waveBuf, stdout)

	if err := cmd.Wait(); err != nil {
		return nil, err
	}

	silkData, err := silk.EncodePcmBuffToSilk(waveBuf.Bytes(), sampleRate, sampleRate, true)
	if err != nil {
		return nil, err
	}

	return silkData, nil
}

func convertFace(face string) string {
	if val, ok := emojis[face]; ok {
		return val;
	}
	return "/" + face;
}

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

func RandomString(len int) string {
	return RandomStringRange(len, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")
}

func RandomStringRange(length int, str string) string {
	sb := strings.Builder{}
	sb.Grow(length)
	for i := 0; i < length; i++ {
		sb.WriteByte(str[rand.Intn(len(str))])
	}
	return sb.String()
}
