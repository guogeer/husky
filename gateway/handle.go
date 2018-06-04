package gateway

import (
	"encoding/json"
	"fmt"
	"github.com/guogeer/husky/cmd"
	"github.com/guogeer/husky/log"
	"net"
)

type Args struct {
	Id, ServerName string

	UId  int
	Data json.RawMessage

	Name string
}

func init() {
	cmd.Bind(FUNC_Route, (*Args)(nil))
	cmd.Bind(FUNC_Broadcast, (*Args)(nil))
	cmd.Bind(FUNC_ServerClose, (*Args)(nil))
	cmd.Bind(FUNC_HelloGateway, (*Args)(nil))

	cmd.Bind(HeartBeat, (*Args)(nil))
	cmd.Bind(FUNC_Close, (*Args)(nil))

	cmd.Bind(FUNC_RegisterServiceInGateway, (*Args)(nil))
}

func FUNC_Close(ctx *cmd.Context, data interface{}) {
	log.Debugf("session close %s", ctx.Ssid)
	if serverName, ok := sessionLocation[ctx.Ssid]; ok {
		ss := &cmd.Session{Id: ctx.Ssid, Out: ctx.Out}
		ss.Route(serverName, "Close", struct{}{})
		delete(sessionLocation, ctx.Ssid)
	}
}

func FUNC_HelloGateway(ctx *cmd.Context, data interface{}) {
	log.Debugf("session locate %s", ctx.Ssid)
	args := data.(*Args)
	uid := args.UId

	ip := "UNKNOW"
	if ss := cmd.GetSession(ctx.Ssid); ss != nil {
		addr := ss.Out.RemoteAddr()
		log.Debug("hello gateway", addr)
		sessionLocation[ctx.Ssid] = args.ServerName
		if host, _, err := net.SplitHostPort(addr); err == nil {
			ip = host
		}
	}
	ss := &cmd.Session{Id: ctx.Ssid, Out: ctx.Out}
	ss.WriteJSON("FUNC_HelloGateway", map[string]interface{}{"UId": uid, "IP": ip})
}

func FUNC_Route(ctx *cmd.Context, data interface{}) {
	args := data.(*Args)
	// log.Info("route", ctx.Ssid)
	if ss := cmd.GetSession(ctx.Ssid); ss != nil {
		client := ctx.Out.(*cmd.Client)
		id := fmt.Sprintf("%s.%s", client.ServerName(), args.Id)
		ss.Out.WriteJSON(id, args.Data)
	}
}

func FUNC_Broadcast(ctx *cmd.Context, data interface{}) {
	args := data.(*Args)
	for _, ss := range cmd.GetSessionList() {
		ss.Out.WriteJSON(args.Id, args.Data)
	}
}

func FUNC_ServerClose(ctx *cmd.Context, data interface{}) {
	for _, ss := range cmd.GetSessionList() {
		client := ctx.Out.(*cmd.Client)
		ss.Out.WriteJSON("ServerClose", map[string]string{"ServerName": client.ServerName()})
	}
}

func HeartBeat(ctx *cmd.Context, data interface{}) {
	ss := cmd.GetSession(ctx.Ssid)
	if ss == nil {
		return
	}
	ss.Out.WriteJSON("HeartBeat", struct{}{})
}

func FUNC_RegisterServiceInGateway(ctx *cmd.Context, data interface{}) {
	args := data.(*Args)
	cmd.RegisterServiceInGateway(args.Name)
}
