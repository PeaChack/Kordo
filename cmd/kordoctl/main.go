//go:build windows

// kordoctl controls the Kordo service from the command line.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"

	"github.com/kordo-vpn/kordo/core/model"
	"github.com/kordo-vpn/kordo/core/rpcapi"
)

const usage = `usage: kordoctl <command> [args]

  status                         engine state and server health
  connect | disconnect
  servers                        list servers
  add <link|@file.conf>          add a server (vless://, ss://, WireGuard .conf)
  rm-server <id>
  test <id>                      test a server
  rules                          list app rules
  route <exe-path|name> <server-id|direct|block> [inherit|direct|ask]
  unroute <rule-id>
  default <server-id|direct>     where unmatched apps go
  killswitch <off|vpn_apps|all>
  killswitch-clear               remove all firewall filters
  activity                       which app goes where
  apps                           running apps
  logs [core]                    tail logs
  call <method> [json-params]    raw RPC call
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	timeout := 2 * time.Second
	conn, err := winio.DialPipe(rpcapi.PipeName, &timeout)
	if err != nil {
		fail(fmt.Errorf("cannot reach the Kordo service (%v); is it installed and running?", err))
	}
	c := rpcapi.NewClient(conn, nil)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := os.Args[2:]
	call := func(method string, params any) json.RawMessage {
		raw, err := c.CallRaw(ctx, method, params)
		if err != nil {
			fail(err)
		}
		return raw
	}
	switch os.Args[1] {
	case "status":
		printJSON(call(rpcapi.MEngineStatus, nil))
	case "connect":
		printJSON(call(rpcapi.MEngineConnect, nil))
	case "disconnect":
		printJSON(call(rpcapi.MEngineDisconn, nil))
	case "servers":
		var servers []model.Server
		mustUnmarshal(call(rpcapi.MServersList, nil), &servers)
		for _, s := range servers {
			fmt.Printf("%s  %-12s  enabled=%-5v  %s\n", s.ID, s.ProviderID, s.Enabled, s.Name)
		}
	case "add":
		need(args, 1)
		link := args[0]
		if strings.HasPrefix(link, "@") {
			b, err := os.ReadFile(link[1:])
			if err != nil {
				fail(err)
			}
			link = string(b)
		}
		printJSON(call(rpcapi.MServersAdd, rpcapi.ServerAdd{Link: link}))
	case "rm-server":
		need(args, 1)
		printJSON(call(rpcapi.MServersRemove, rpcapi.IDParam{ID: args[0]}))
	case "test":
		need(args, 1)
		printJSON(call(rpcapi.MServersTest, rpcapi.IDParam{ID: args[0]}))
	case "rules":
		var rules []model.AppRule
		mustUnmarshal(call(rpcapi.MRulesList, nil), &rules)
		for _, r := range rules {
			t := r.Target.Kind
			if r.Target.ServerID != "" {
				t += ":" + r.Target.ServerID
			}
			fmt.Printf("%s  %-14s  %-8s  %-16s  %s\n", r.ID, t, r.Children, r.Match.Kind, r.Match.Value)
			for _, l := range r.Learned {
				fmt.Printf("      └ learned %s → %s\n", l.Match.Value, l.Target.Kind)
			}
		}
	case "route":
		need(args, 2)
		m := model.Match{Kind: model.MatchName, Value: args[0]}
		if strings.ContainsAny(args[0], `\/`) {
			m = model.Match{Kind: model.MatchPath, Value: args[0]}
		}
		r := model.AppRule{Match: m, Target: target(args[1]), Children: model.ChildInherit}
		if len(args) > 2 {
			r.Children = model.ChildPolicy(args[2])
		}
		printJSON(call(rpcapi.MRulesUpsert, r))
	case "unroute":
		need(args, 1)
		printJSON(call(rpcapi.MRulesRemove, rpcapi.IDParam{ID: args[0]}))
	case "default":
		need(args, 1)
		var p model.Profile
		mustUnmarshal(call(rpcapi.MProfileGet, nil), &p)
		p.DefaultTarget = target(args[0])
		printJSON(call(rpcapi.MProfileSet, rpcapi.ProfileSet{Profile: p}))
	case "killswitch":
		need(args, 1)
		printJSON(call(rpcapi.MKillSwitchSet, rpcapi.KillSwitchSet{Mode: model.KillMode(args[0])}))
	case "killswitch-clear":
		printJSON(call(rpcapi.MKillSwitchClear, nil))
	case "activity":
		var list []rpcapi.ActivityEntry
		mustUnmarshal(call(rpcapi.MActivity, nil), &list)
		for _, e := range list {
			fmt.Printf("%-60s → %-16s conns=%d ↑%d ↓%d %v\n", e.App, e.Target, e.Conns, e.Upload, e.Download, e.Hosts)
		}
	case "apps":
		var list []rpcapi.RunningApp
		mustUnmarshal(call(rpcapi.MAppsRunning, nil), &list)
		for _, a := range list {
			fmt.Printf("%6d  %s\n", a.PID, a.Path)
		}
	case "logs":
		file := "service"
		if len(args) > 0 {
			file = args[0]
		}
		var text string
		mustUnmarshal(call(rpcapi.MLogsTail, rpcapi.LogsTail{Lines: 200, File: file}), &text)
		fmt.Println(text)
	case "call":
		need(args, 1)
		var params any
		if len(args) > 1 {
			params = json.RawMessage(args[1])
		}
		printJSON(call(args[0], params))
	default:
		fmt.Print(usage)
		os.Exit(2)
	}
}

func target(s string) model.Target {
	switch s {
	case "direct":
		return model.Direct()
	case "block":
		return model.Target{Kind: model.TargetBlock}
	}
	return model.ToServer(s)
}

func need(args []string, n int) {
	if len(args) < n {
		fmt.Print(usage)
		os.Exit(2)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func mustUnmarshal(raw json.RawMessage, v any) {
	if err := json.Unmarshal(raw, v); err != nil {
		fail(err)
	}
}

func printJSON(raw json.RawMessage) {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		fmt.Println(string(raw))
		return
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
