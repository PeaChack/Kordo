//go:build android

package mobile

// The Android library is bound together with sing-box's libbox (VpnService
// glue: TUN, protect(), connection owners, network monitor) by sagernet's
// gomobile fork; these imports keep both in go.mod.
import (
	_ "github.com/sagernet/gomobile/bind"
	_ "github.com/sagernet/sing-box/experimental/libbox"
)
