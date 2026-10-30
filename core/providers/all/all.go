// Package all registers every built-in provider. Import it for side effects.
package all

import (
	_ "github.com/PeaChack/Kordo/core/providers/shadowsocks"
	_ "github.com/PeaChack/Kordo/core/providers/vless"
	_ "github.com/PeaChack/Kordo/core/providers/wireguard"
)
