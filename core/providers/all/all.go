// Package all registers every built-in provider. Import it for side effects.
package all

import (
	_ "github.com/kordo-vpn/kordo/core/providers/shadowsocks"
	_ "github.com/kordo-vpn/kordo/core/providers/vless"
	_ "github.com/kordo-vpn/kordo/core/providers/wireguard"
)
