package cmd

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/buger/jsonparser"
	"github.com/guogeer/quasar/log"
)

var (
	ErrInvalidSign     = errors.New("invalid sign")
	errPackageExpire   = errors.New("package expire")
	errTooLargeMessage = errors.New("too large message")
)

type Context struct {
	Out         Conn   // 连接
	MsgId       string // 消息ID
	Ssid        string // 发送方会话ID
	Version     int    // 协议版本，当前未生效
	ServerName  string // 请求的协议头
	ClientAddr  string // 客户端地址
	MatchServer string // 多个服务合并后的唯一serverName
	isFail      bool   // 失败处理后，不需要继续处理
}

func (ctx *Context) Fail() {
	ctx.isFail = true
}

type Message struct {
	id   string
	h    Handler
	hook Handler
	ctx  *Context
	args interface{}
}

type SafeQueue struct {
	q chan interface{}
}

func NewSafeQueue(size int) *SafeQueue {
	return &SafeQueue{q: make(chan interface{}, size)}
}

func (h *SafeQueue) Enqueue(i interface{}) {
	h.q <- i
}

func (h *SafeQueue) Dequeue(delay time.Duration) interface{} {
	if delay > 0 {
		select {
		case msg := <-h.q:
			return msg
		case <-time.After(delay):
			return nil
		}
	}
	if delay == 0 {
		select {
		case msg := <-h.q:
			return msg
		default:
			return nil
		}
	}
	if delay < 0 {
		msg := <-h.q
		return msg

	}
	return nil
}

var defaultMessageQueue = NewSafeQueue(16 << 10)

func GetMessageQueue() *SafeQueue {
	return defaultMessageQueue
}

// 统计消息平均负载&访问频率等
type messageStat struct {
	id   string
	d    time.Duration // 耗时
	call int           // 调用次数
}

func (stat *messageStat) merge(stat2 *messageStat) {
	if stat2 != nil {
		stat.d += stat2.d
		stat.call += stat2.call
	}
}

var (
	lastPrintTime time.Time // 10分钟打印一次
	messageStats  map[string]messageStat
)

// TODO 暂时未考虑并发访问
func waitAndRunOnce(loop int, delay time.Duration) {
	var t1, t2 time.Time
	var stats map[string]messageStat
	if enableDebug {
		stats = map[string]messageStat{}
	}
	for i := 0; i < loop; i++ {
		front := GetMessageQueue().Dequeue(delay)
		if front == nil {
			break
		}
		if enableDebug {
			t1 = time.Now()
		}
		msg := front.(*Message)
		if msg.hook != nil {
			msg.hook(msg.ctx, msg.args)
		}
		if !msg.ctx.isFail {
			msg.h(msg.ctx, msg.args)
		}

		if enableDebug {
			t2 = time.Now()
			stat := stats[msg.id]
			stat.merge(&messageStat{d: t2.Sub(t1), call: 1})
			stats[msg.id] = stat
		}
	}
	if enableDebug {
		if lastPrintTime.IsZero() {
			lastPrintTime = time.Now()
		}
		if len(messageStats) == 0 {
			messageStats = map[string]messageStat{}
		}

		for id, stat := range stats {
			stat2 := messageStats[id]
			stat2.merge(&stat)
			messageStats[id] = stat2
		}
		tpc := make([]messageStat, 0, 256) // cost time per call
		cps := make([]messageStat, 0, 256) // call per second
		for id, stat := range messageStats {
			stat.id = id
			tpc = append(tpc, stat)
			cps = append(cps, stat)
		}

		d := time.Since(lastPrintTime)
		if d >= 10*time.Minute {
			log.Debug("=========== message stats start  ============")
			sort.SliceStable(tpc, func(i, j int) bool {
				return tpc[i].d.Seconds()/float64(tpc[i].call) > tpc[j].d.Seconds()/float64(tpc[j].call)
			})
			sort.SliceStable(cps, func(i, j int) bool { return cps[i].call > cps[j].call })
			for i := 0; i < 10 && i < len(messageStats); i++ {
				stat1, stat2 := tpc[i], cps[i]
				log.Debugf("cost time per call: %s %.2fms, call per second %s %.2f", stat1.id, stat1.d.Seconds()*1000/float64(stat1.call), stat2.id, float64(stat2.call)/d.Seconds())
			}
			log.Debug("=========== message stats end  ============")

			// 清理旧数据
			messageStats = nil
			lastPrintTime = time.Time{}
		}
	}
}

func RunOnce() {
	waitAndRunOnce(256, 40*time.Millisecond)
}

func Enqueue(ctx *Context, h Handler, args interface{}) {
	GetMessageQueue().Enqueue(&Message{ctx: ctx, h: h, args: args})
}

type Package struct {
	Id         string          `json:",omitempty"`    // 消息ID
	Data       json.RawMessage `json:",omitempty"`    // 数据,object类型
	Sign       string          `json:",omitempty"`    // 签名
	Ssid       string          `json:",omitempty"`    // 会话ID
	Version    int             `json:"Ver,omitempty"` // 版本
	ExpireTs   int64           `json:",omitempty"`    // 发送的时间戳
	ServerName string          `json:",omitempty"`    // 请求的协议头
	ClientAddr string          `json:",omitempty"`    // 客户端地址

	Body     interface{} `json:"-"` // 传入的参数
	IsZip    bool        `json:"-"`
	SignType string      `json:"-"` // md5,raw
}

func (pkg *Package) parser(typ string) *hashParser {
	parser := defaultHashParser
	switch typ {
	case "md5":
		parser = defaultAuthParser
	case "raw":
		parser = defaultRawParser
	}
	return parser
}

func (pkg *Package) Encode() ([]byte, error) {
	parser := pkg.parser(pkg.SignType)
	return parser.Encode(pkg)
}

func (pkg *Package) Decode(buf []byte) error {
	parser := pkg.parser(pkg.SignType)
	if _, err := parser.Decode(buf); err != nil {
		return err
	}
	if pkg.Body != nil {
		return json.Unmarshal(buf, pkg.Body)
	}
	return nil
}

var defaultRawParser = &hashParser{}
var defaultHashParser = &hashParser{
	ref:      []int{0, 3, 4, 8, 10, 11, 13, 14},
	key:      "helloworld!",
	tempSign: "12345678",
}
var defaultAuthParser = &hashParser{
	key:      "420e57b017066b44e05ea1577f6e2e12",
	tempSign: "a9542bb104fe3f4d562e1d275e03f5ba",
}

// 哈希
type hashParser struct {
	ref             []int
	key             string
	tempSign        string
	compressPackage int // 压缩数据
}

func (parser *hashParser) Encode(pkg *Package) ([]byte, error) {
	pkg.Sign = parser.tempSign
	body, err := marshalJSON(pkg.Body)
	if err != nil {
		return nil, err
	}
	pkg.Data = body
	if n := parser.compressPackage; pkg.IsZip && n > 0 && n < len(pkg.Data) {
		b := bytes.Buffer{}
		w := zlib.NewWriter(&b)
		w.Write(pkg.Data)
		w.Close()

		zipData := base64.StdEncoding.EncodeToString(b.Bytes())
		pkg.Data = json.RawMessage(`"` + zipData + `"`)
	}

	buf, err := json.Marshal(pkg)
	if err != nil {
		return nil, err
	}
	if _, err := parser.Signature(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (parser *hashParser) Decode(buf []byte) (*Package, error) {
	pkg := &Package{}
	if err := json.Unmarshal(buf, pkg); err != nil {
		return nil, err
	}
	if ts := pkg.ExpireTs; ts > 0 && ts < time.Now().Unix() {
		return nil, errPackageExpire
	}

	sign, err := parser.Signature(buf)
	if n := len(pkg.Data); pkg.IsZip && n > 1 {
		buf, err := base64.StdEncoding.DecodeString(string(pkg.Data[1 : n-1]))
		if err != nil {
			return nil, err
		}
		r, err := zlib.NewReader(bytes.NewReader(buf))
		if err != nil {
			return nil, err
		}

		raw := bytes.Buffer{}
		io.Copy(&raw, r)
		r.Close()
		pkg.Data = raw.Bytes()
	}
	if err != nil {
		return pkg, ErrInvalidSign
	}
	if sign != "" && pkg.Sign != sign {
		return pkg, ErrInvalidSign
	}
	return pkg, nil
}

func (parser *hashParser) Signature(data []byte) (string, error) {
	ref, key := parser.ref, parser.key
	if key == "" {
		return "", nil
	}
	buf := append([]byte(key), data...)
	tempSign := parser.tempSign
	_, _, n, err := jsonparser.Get(data, "Sign")
	if err != nil {
		return "", err
	}
	signLen := len(tempSign) + 1
	if n < signLen {
		return "", ErrInvalidSign
	}
	copy(buf[len(key)+n-signLen:], tempSign)

	sum := md5.Sum(buf)
	sign := hex.EncodeToString(sum[:])
	if len(ref) == len(tempSign) {
		sign2 := make([]byte, len(ref))
		for k, v := range ref {
			sign2[k] = sign[v]
		}
		sign = string(sign2)
	}
	copy(data[n-signLen:n], sign)
	return sign, nil
}

func Encode(name string, i interface{}) ([]byte, error) {
	pkg := &Package{Id: name, Body: i}
	return pkg.Encode()
}

func Decode(buf []byte) (*Package, error) {
	return defaultHashParser.Decode(buf)
}

func marshalJSON(i interface{}) ([]byte, error) {
	switch v := i.(type) {
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	}
	return json.Marshal(i)
}

func routeMessage(server, message string) (string, string) {
	if server != "" {
		message = server + "." + message
	}
	if subs := strings.SplitN(message, ".", 2); len(subs) > 1 {
		server, message = subs[0], subs[1]
	}
	return server, message
}
