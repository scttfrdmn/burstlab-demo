// Package jobs embeds the demo job scripts so `burstlab-demo push-jobs` can
// install them in alice's home directory on the head node.
package jobs

import "embed"

//go:embed *.sh
var FS embed.FS
