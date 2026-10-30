package rpcapi

import (
	"encoding/json"
	"time"

	"github.com/PeaChack/Kordo/core/model"
)

const PipeName = `\\.\pipe\kordo`

// Method names.
const (
	MHello           = "rpc.hello"
	MProvidersList   = "providers.list"
	MServersList     = "servers.list"
	MServersAdd      = "servers.add"
	MServersUpdate   = "servers.update"
	MServersRemove   = "servers.remove"
	MServersTest     = "servers.test"
	MRulesList       = "rules.list"
	MRulesUpsert     = "rules.upsert"
	MRulesRemove     = "rules.remove"
	MProfileGet      = "profile.get"
	MProfileSet      = "profile.set"
	MAppsRunning     = "apps.running"
	MAppsInstalled   = "apps.installed"
	MEngineConnect   = "engine.connect"
	MEngineDisconn   = "engine.disconnect"
	MEngineStatus    = "engine.status"
	MKillSwitchSet   = "killswitch.set"
	MKillSwitchClear = "killswitch.clear"
	MLogsTail        = "logs.tail"
	MChildrenAnswer  = "children.answer"
	MChildrenForget  = "children.forget"
	MActivity        = "activity.list"
	MDiagnostics     = "diagnostics.collect"
)

// Notifications.
const (
	NStateChanged = "state.changed"
	NChildAsk     = "child.ask"
	NDataChanged  = "data.changed" // servers/rules/profile changed by another client
)

type Hello struct {
	ProtocolVersion int    `json:"protocolVersion"`
	Version         string `json:"version"`
	EngineVersion   string `json:"engineVersion"`
	IsAdmin         bool   `json:"isAdmin"`
}

type ProviderInfo struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"displayName"`
	Schemes     []string        `json:"schemes"`
	Schema      json.RawMessage `json:"schema"`
}

type ServerAdd struct {
	Link     string          `json:"link,omitempty"` // vless://, ss://, WireGuard .conf text
	Name     string          `json:"name,omitempty"`
	Provider string          `json:"provider,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
}

type IDParam struct {
	ID string `json:"id"`
}

type TestResult struct {
	ID        string `json:"id"`
	TCPms     int64  `json:"tcpMs"`
	TCPError  string `json:"tcpError,omitempty"`
	HTTPms    int64  `json:"httpMs"`
	HTTPError string `json:"httpError,omitempty"`
	Via       string `json:"via"` // "running engine" | "temporary engine" | "tcp only"
}

type RunningApp struct {
	PID     uint32 `json:"pid"`
	PPID    uint32 `json:"ppid"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Store   string `json:"store,omitempty"` // store package name if under WindowsApps
	Session uint32 `json:"session"`
	System  bool   `json:"system"` // cannot be selected
}

type InstalledApp struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Publisher string `json:"publisher,omitempty"`
	Store     string `json:"store,omitempty"`
}

type KillSwitchSet struct {
	Mode model.KillMode `json:"mode"`
}

type LogsTail struct {
	Lines int    `json:"lines"`
	File  string `json:"file"` // "service" | "core"
}

type ChildAsk struct {
	AskID      string      `json:"askId"`
	RuleID     string      `json:"ruleId"`
	ParentPID  uint32      `json:"parentPid"`
	ParentPath string      `json:"parentPath"`
	ChildPID   uint32      `json:"childPid"`
	ChildPath  string      `json:"childPath"`
	Child      model.Match `json:"child"`
	Shared     bool        `json:"shared"`
	Time       time.Time   `json:"time"`
}

type ChildAnswer struct {
	AskID    string `json:"askId"`
	Choice   string `json:"choice"`   // "parent" (same as parent) | "direct"
	Remember bool   `json:"remember"` // false = only this time (not supported for path rules: treated as remember)
}

type ActivityEntry struct {
	App      string   `json:"app"`      // process path
	Target   string   `json:"target"`   // server name, "direct" or "block"
	TargetID string   `json:"targetId"` // server id or ""
	Conns    int      `json:"conns"`
	Upload   int64    `json:"upload"`
	Download int64    `json:"download"`
	Hosts    []string `json:"hosts"`
}

type ProfileSet struct {
	Profile model.Profile `json:"profile"` // Rules are ignored; use rules.* methods
}

type StateNote struct {
	State      string `json:"state"`
	KillSwitch string `json:"killSwitch"`
	Status     any    `json:"status"`
}
